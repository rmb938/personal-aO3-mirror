package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// Helpers shared by the API commands: request options, error reporting and output formatting.

// reveal returns the request option for a command's --names flag: it asks the API for real
// names on this one request (the API hashes them otherwise). Without --names it changes nothing.
func reveal(names bool) apiclient.RequestOption {
	return apiclient.WithEditRequest(func(req *http.Request) error {
		if names {
			q := req.URL.Query()
			q.Set(runtime.RevealParam, "1")
			req.URL.RawQuery = q.Encode()
		}
		return nil
	})
}

// fail reports a failed API call as "<what>: <reason>" and returns exit code 1.
func fail(errOut io.Writer, what string, err error) int {
	fmt.Fprintf(errOut, "%s: %s\n", what, apiError(err))
	return 1
}

// apiError describes an API call error: a problem response by its title, detail and invalid
// parts, a missing admin socket with a hint to start the API.
func apiError(err error) string {
	if p, ok := errors.AsType[*apiclient.ProblemStatusCodeWithHeaders](err); ok {
		return problemText(p.Response)
	}
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return err.Error() + " (is the API running? start it with `ao3mirror serve --no-reveal`)"
	}
	return err.Error()
}

// problemText is a problem's title and detail, then one "  <where>: <detail>" line per invalid
// part of a validation error.
func problemText(p apiclient.Problem) string {
	var b strings.Builder
	b.WriteString(p.Title)
	if d, ok := p.Detail.Get(); ok {
		b.WriteString(": " + d)
	}
	for _, e := range p.Errors {
		b.WriteString("\n  " + e.Pointer.Or(e.Parameter.Or("?")) + ": " + e.Detail)
	}
	return b.String()
}

// idFromLocation returns the trailing id of a Location path such as /api/v1/sources/3.
func idFromLocation(loc string) (int64, bool) {
	id, err := strconv.ParseInt(path.Base(loc), 10, 64)
	return id, err == nil && id > 0
}

// parseID parses a positional id argument, reporting a bad one on errOut. apiclient.ID is an
// int, so it parses with Atoi: no narrowing conversion.
func parseID(errOut io.Writer, s string) (apiclient.ID, bool) {
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		fmt.Fprintf(errOut, "bad id %q\n", s)
		return 0, false
	}
	return apiclient.ID(id), true
}

// jobError is a job's last error as "code: detail", or "-".
func jobError(code apiclient.OptJobErrorCode, detail apiclient.OptString) string {
	c, d := string(code.Or("")), detail.Or("")
	switch {
	case c == "":
		return dash(d)
	case d == "":
		return c
	default:
		return c + ": " + d
	}
}

// counts formats per-value counts as "a=1 b=2", in the API's order; "-" when empty.
func counts[T any](items []T, kv func(T) (string, int)) string {
	if len(items) == 0 {
		return "-"
	}
	parts := make([]string, len(items))
	for i, it := range items {
		k, n := kv(it)
		parts[i] = k + "=" + comma(n)
	}
	return strings.Join(parts, " ")
}

// setFields is the JSON of v without its null members (recursively) or the named top-level keys:
// the fields that were set, keys sorted.
func setFields(v json.Marshaler, drop ...string) (string, error) {
	b, err := v.MarshalJSON()
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "", err
	}
	for _, k := range drop {
		delete(m, k)
	}
	b, err = json.Marshal(dropNulls(m))
	return string(b), err
}

func dropNulls(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if e == nil {
				delete(x, k)
				continue
			}
			x[k] = dropNulls(e)
		}
	case []any:
		for i, e := range x {
			x[i] = dropNulls(e)
		}
	}
	return v
}

// nilTotal is a count that is null until known: "?" then.
func nilTotal(n apiclient.NilInt) string {
	if n.Null {
		return "?"
	}
	return comma(n.Value)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// when formats an optional timestamp in local time to the minute, or "-".
func when(t apiclient.OptTimestamp) string {
	if !t.Set {
		return "-"
	}
	return whenTime(time.Time(t.Value))
}

func whenTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// clock formats a timestamp in local time to the second, without the year.
func clock(t apiclient.Timestamp) string {
	return time.Time(t).Local().Format("01-02 15:04:05")
}

// localTime formats an optional timestamp in local time to the second, or "-".
func localTime(t apiclient.OptTimestamp) string {
	if !t.Set {
		return "-"
	}
	return time.Time(t.Value).Local().Format("2006-01-02 15:04:05")
}

// date formats an optional calendar date, or "-".
func date(d apiclient.OptDate) string {
	if !d.Set {
		return "-"
	}
	return d.Value.Format(time.DateOnly)
}

func comma[T ~int | ~int64](n T) string {
	s := strconv.FormatInt(int64(n), 10)
	if n < 0 {
		return s
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func human(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%.1f days", d.Hours()/24)
	case d >= time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	default:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	}
}

func mb[T ~int | ~int64](n T) string {
	b := int64(n)
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1<<30))
	}
	return fmt.Sprintf("%d MB", (b+(1<<20)-1)>>20)
}

// idList joins up to max ids with commas, then "+N more"; "-" when empty.
func idList[T ~int | ~int64](xs []T, max int) string {
	if len(xs) == 0 {
		return "-"
	}
	n := min(len(xs), max)
	parts := make([]string, n)
	for i := range n {
		parts[i] = strconv.FormatInt(int64(xs[i]), 10)
	}
	s := strings.Join(parts, ",")
	if len(xs) > n {
		s += fmt.Sprintf(" +%d more", len(xs)-n)
	}
	return s
}
