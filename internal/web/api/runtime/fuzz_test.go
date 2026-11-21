package runtime

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/config"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/searchquery"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/problem"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/request"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/route"
)

// The fuzz targets here check the param and body parsers against independent oracles: what
// each input must give is computed from the documented rules, not by calling the parser.

// fuzzAllowed is the allowlist FuzzParseParams parses with.
var fuzzAllowed = []string{string(dto.ParamPage), string(dto.ParamPerPage), string(dto.ParamSort)}

// oracleInt is ParseInt's rule: ASCII digits only, the value in [1, hi].
func oracleInt(s string, hi int64) (int64, bool) {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, false
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v < 1 || v > math.MaxInt64 || int64(v) > hi {
		return 0, false
	}
	return int64(v), true
}

// oracleText is problem.ValidText's rule: valid UTF-8, at most maxRunes runes, no control
// characters.
func oracleText(s string, maxRunes int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= maxRunes && !strings.ContainsFunc(s, unicode.IsControl)
}

// requireCatalogued requires every detail of e (and its items) to come from the catalogue.
func requireCatalogued(t *testing.T, e *problem.Error) {
	t.Helper()
	cat := problem.Catalogue()
	details := []problem.Detail{e.Detail}
	for _, it := range e.Errors {
		require.NotEmpty(t, it.Detail.Message(), "every item explains itself")
		details = append(details, it.Detail)
	}
	for _, d := range details {
		if d.Message() != "" {
			_, ok := slices.BinarySearch(cat, d.Message())
			require.True(t, ok, "detail %q not in the catalogue", d.Message())
		}
	}
}

// problemOf requires err to be a *problem.Error with catalogue details.
func problemOf(t *testing.T, err error) *problem.Error {
	t.Helper()
	e, ok := errors.AsType[*problem.Error](err)
	require.True(t, ok, "want *problem.Error, got %v", err)
	requireCatalogued(t, e)
	return e
}

func itemParams(e *problem.Error) []string {
	out := make([]string, len(e.Errors))
	for i, it := range e.Errors {
		out[i] = it.Param
	}
	return out
}

// FuzzParseParams checks ParseParams, Page and Enum against the documented rules: the exact
// invalid parameters in order, the values handed on, and a valid page within the offset cap.
func FuzzParseParams(f *testing.F) {
	for _, s := range []string{
		"", "page=2&per_page=10", "page=1&page=2", RevealParam + "=1&sort=title", RevealParam + "=0",
		"zz=1&SENTINEL=x&sort=SENTINEL", "%zz", "a;b", "a=%", strings.Repeat("page=1&", MaxQueryParams+1),
		"page=+1", "page=-1", "page=01", "per_page=0", "page=1001&per_page=100", "page=1002&per_page=100",
		"page=9223372036854775808&per_page=1", "page=100001", "page=100002", "sort=best_match", "sort=",
		"page=&per_page=", "per_page=x&page=y&sort=z", "page=1&Page=1", "page=1+",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		r.URL.RawQuery = raw
		p, err := ParseParams(r, fuzzAllowed...)

		vals, qerr := url.ParseQuery(raw)
		n := 0
		for _, vs := range vals {
			n += len(vs)
		}
		if qerr != nil || n > MaxQueryParams {
			require.Equal(t, dto.TypeBadRequest, problemOf(t, err).Type)
			return
		}
		require.NoError(t, err)

		var want []string
		values := map[string]string{}
		for _, name := range slices.Sorted(func(yield func(string) bool) {
			for k := range vals {
				if !yield(k) {
					return
				}
			}
		}) {
			switch {
			case name != RevealParam && !slices.Contains(fuzzAllowed, name), len(vals[name]) > 1:
				want = append(want, name)
			case name != RevealParam:
				values[name] = vals[name][0]
			}
		}
		require.Equal(t, values, p.Values())

		pg := p.Page()
		per, perOK := int64(DefaultPerPage), true
		if s, ok := values[string(dto.ParamPerPage)]; ok {
			per, perOK = oracleInt(s, MaxPerPage)
		}
		page, pageOK := int64(1), true
		if s, ok := values[string(dto.ParamPage)]; ok {
			page, pageOK = oracleInt(s, MaxOffset+1)
		}
		if !perOK {
			want = append(want, string(dto.ParamPerPage))
		}
		if !pageOK || (perOK && (page-1)*per > MaxOffset) {
			want = append(want, string(dto.ParamPage))
		}
		if perOK && pageOK {
			require.Equal(t, Page{Page: page, PerPage: per}, pg)
		}

		sort := p.Enum(string(dto.ParamSort), dto.WorkSort(""), DetailWorkSort)
		if s, ok := values[string(dto.ParamSort)]; ok && !slices.Contains(dto.WorkSort("").EnumValues(), s) {
			want = append(want, string(dto.ParamSort))
			require.Empty(t, sort)
		} else {
			require.Equal(t, values[string(dto.ParamSort)], sort)
		}

		if len(want) == 0 {
			require.NoError(t, p.Err())
			require.GreaterOrEqual(t, pg.Offset(), int64(0))
			require.LessOrEqual(t, pg.Offset(), int64(MaxOffset))
			return
		}
		e := problemOf(t, p.Err())
		require.Equal(t, dto.TypeValidation, e.Type)
		require.Equal(t, want, itemParams(e))
	})
}

// FuzzPathParams checks ParseInt and the path readers (ID, tag name, SHA-256) against their
// rules, with the tag either as chi hands it unescaped (RawPath empty) or still escaped.
func FuzzPathParams(f *testing.F) {
	sha := strings.Repeat("0123456789abcdef", 4)
	f.Add("1", int64(10), "Fixture*s*Tag", sha, false)
	f.Add("9007199254740991", int64(MaxID), "a%2Fb*d*c", strings.ToUpper(sha), true)
	f.Add("9007199254740992", int64(-1), "%zz", sha[1:], true)
	f.Add("+1", int64(1), "", sha+"0", false)
	f.Add("-1", int64(0), "x\u0000y", "g"+sha[1:], false)
	f.Add("01", int64(math.MaxInt64), strings.Repeat("é", MaxTagNameRunes+1), "", false)
	f.Add("1_0", int64(100), "*s*", sha, true)
	f.Fuzz(func(t *testing.T, id string, hi int64, tag, sha string, escaped bool) {
		v, ok := ParseInt(id, hi)
		wv, wok := oracleInt(id, hi)
		require.Equal(t, wok, ok)
		require.Equal(t, wv, v)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", id)
		rctx.URLParams.Add("name", tag)
		rctx.URLParams.Add("sha256", sha)
		r := httptest.NewRequest(http.MethodGet, "/x", nil)
		if escaped {
			r.URL.RawPath = "/x" // chi routed on the escaped path
		}
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
		p, err := ParseParams(r)
		require.NoError(t, err)

		var want []string
		gotID := p.PathID("id")
		wantID, idOK := oracleInt(id, MaxID)
		require.Equal(t, wantID, gotID)
		if !idOK {
			want = append(want, "id")
		}

		gotTag := p.PathTagName("name")
		dec, derr := tag, error(nil)
		if escaped {
			dec, derr = url.PathUnescape(tag)
		}
		wantTag := ao3url.TagFromParam(dec)
		if derr != nil || wantTag == "" || !oracleText(wantTag, MaxTagNameRunes) {
			want = append(want, "name")
			wantTag = ""
		}
		require.Equal(t, wantTag, gotTag)

		gotSHA := p.PathSHA256("sha256")
		wantSHA := sha
		if len(sha) != 64 || strings.ContainsFunc(sha, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) {
			want = append(want, "sha256")
			wantSHA = ""
		}
		require.Equal(t, wantSHA, gotSHA)

		if len(want) == 0 {
			require.NoError(t, p.Err())
			return
		}
		e := problemOf(t, p.Err())
		require.Equal(t, want, itemParams(e))
	})
}

// fuzzAPI builds an API with only GET /api/v1/healthz (no parameters), rate limits too high to
// trip and its log in the returned buffer.
func fuzzAPI(f *testing.F, noReveal bool, logs *bytes.Buffer) *API {
	f.Helper()
	log := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	st, err := store.Open(f.Context(), f.TempDir(), store.Options{Logger: slog.New(slog.DiscardHandler)})
	require.NoError(f, err)
	f.Cleanup(func() { require.NoError(f, st.Close()) })
	cfg := config.Default()
	ctrls := controllers.New(controllers.Options{Store: st, Logger: log, Sources: cfg.Sources, Interval: cfg.Rate.Interval})
	opts := Options{
		Version: "test", TrustedProxies: []string{"127.0.0.1/32"}, AllowedHosts: testHosts, RequestTimeout: 10 * time.Second,
		GeneralPerMin: math.MaxInt32, SourceAddPerHour: math.MaxInt32, SearchPerMin: math.MaxInt32, NoReveal: noReveal,
	}
	a, err := New(opts, log, ctrls, func(a *API, _ *controllers.Controllers, r *chi.Mux, _ bool) {
		r.Get(route.Healthz, a.Handle(func(req *http.Request) (any, error) {
			if err := NoParams(req); err != nil {
				return nil, err
			}
			return dto.Health{Status: dto.HealthOK}, nil
		}))
	})
	require.NoError(f, err)
	return a
}

// FuzzRequestQuery sends any raw query through the public middleware chain to a route without
// parameters and checks the outcome against the documented order: URL cap (414), malformed
// query (400), the reveal parameter (exactly "1", once; refused with --no-reveal), then the
// route's own check. Only a valid reveal parameter reveals, and no query value reaches the body,
// headers or logs.
func FuzzRequestQuery(f *testing.F) {
	for _, s := range []string{
		"", RevealParam + "=1", RevealParam + "=1&" + RevealParam + "=1", RevealParam + "=SENTINEL-1",
		RevealParam + "=1&x=SENTINEL-2", "x=SENTINEL-3", "%zz=SENTINEL-4", RevealParam + "=%31",
		RevealParam + "=1&" + strings.Repeat("a=SENTINEL-5&", MaxQueryParams), strings.Repeat("a", MaxURLBytes),
		RevealParam + "=+1", RevealParam + "=1;x=SENTINEL-6", RevealParam + "= 1", "SENTINEL-7=1",
		RevealParam + "=1&SENTINEL-8=1&SENTINEL-8=2",
	} {
		f.Add(s, false)
		f.Add(s, true)
	}
	var logs bytes.Buffer
	apis := map[bool]*API{false: fuzzAPI(f, false, &logs), true: fuzzAPI(f, true, &logs)}
	f.Fuzz(func(t *testing.T, raw string, noReveal bool) {
		logs.Reset()
		r := httptest.NewRequest(http.MethodGet, route.Healthz, nil)
		r.URL.RawQuery = raw
		w := httptest.NewRecorder()
		apis[noReveal].Public().ServeHTTP(w, r)

		wantType, revealed := expectQuery(raw, noReveal)
		require.Equal(t, map[bool]string{false: "hashed", true: "revealed"}[revealed], w.Header().Get(HeaderFanwork))
		require.True(t, jsontext.Value(w.Body.Bytes()).IsValid(), "body is valid JSON")
		if wantType == "" {
			require.Equal(t, http.StatusOK, w.Code)
		} else {
			var p dto.Problem
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
			require.Equal(t, wantType, p.Type)
			require.Equal(t, w.Code, p.Status)
		}
		require.NotContains(t, w.Body.String(), "SENTINEL")
		for k, vs := range w.Header() {
			for _, v := range vs {
				require.NotContains(t, v, "SENTINEL", k)
			}
		}
		require.NotContains(t, logs.String(), "SENTINEL")
	})
}

// expectQuery is the problem type GET healthz?raw must give ("" for 200) and whether it is
// served revealed.
func expectQuery(raw string, noReveal bool) (dto.ProblemType, bool) {
	if len(route.Healthz)+len(raw) > MaxURLBytes {
		return dto.TypeURITooLong, false
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return dto.TypeBadRequest, false
	}
	revealed := false
	if vs, ok := vals[RevealParam]; ok {
		switch {
		case len(vs) != 1 || vs[0] != "1":
			return dto.TypeValidation, false
		case noReveal:
			return dto.TypeRevealDisabled, false
		}
		revealed = true
	}
	n := 0
	for _, vs := range vals {
		n += len(vs)
	}
	switch {
	case n > MaxQueryParams:
		return dto.TypeBadRequest, revealed
	case len(vals) > 1 || (len(vals) == 1 && !revealed):
		return dto.TypeValidation, revealed
	}
	return "", revealed
}

// FuzzDecodeJSON decodes any body as each request type (picked by kind) through Handle's body
// cap. A body is accepted only when it is one valid JSON object; every recorded problem points
// at a member the body has, with a catalogue detail; and a body with no problem is within every
// documented bound of its type.
func FuzzDecodeJSON(f *testing.F) {
	types := request.Types()
	for _, s := range []string{
		`{}`, `null`, `[]`, `"x"`, `1`, ``, ` {} `, `{}{}`, `{"x":1}`, `{"url":"a","url":"b"}`, "{\"q\":\"\xff\"}",
		`{"url":"https://archiveofourown.org/tags/Fixture%20Tag/works","order":"kudos_desc","priority":-1000,"poll_interval":"10m"}`,
		`{"url":"/works/1","priority":1001,"poll_interval":"1s","confirm":true}`,
		`{"url":"/users/fixture_author/pseuds/x","order":"nope","poll_interval":"721h"}`,
		`{"full":true}`, `{"priority":null,"poll_interval":"1h"}`, `{"priority":5}`,
		`{"q":"fixture -lorem \"ipsum dolor\"","tag_ids":[1,2],"exclude_tag_ids":[3],"complete":true,"crossover":false,` +
			`"single_chapter":true,"language":"English","statuses":["fetched"],"fetched":true,` +
			`"words":{"min":1,"max":2},"kudos":{"min":0},"revised":{"from":"2020-01-01","to":"2020-12-31"},` +
			`"published":{"to":"2021-02-28"},"sort":"best_match","direction":"asc"}`,
		`{"q":"-x","tag_ids":[0,1,1,9007199254740992],"words":{"min":3,"max":2},"revised":{"from":"2020-02-30"},"sort":"best_match"}`,
		`{"statuses":["x","fetched","fetched"],"hits":{"max":-1},"published":{"from":"2021-01-02","to":"2021-01-01"}}`,
		`{"name":"fixture tag","types":["fandom","fandom","x"],"canonical":true,"parent_ids":[1],"sort":"name","direction":"desc"}`,
		`{"name":" ","sort":"works","direction":"up"}`,
	} {
		for k := range types {
			f.Add(uint8(k), []byte(s))
		}
	}
	f.Add(uint8(0), bytes.Repeat([]byte(" "), MaxBodyBytes+1))
	f.Fuzz(func(t *testing.T, kind uint8, body []byte) {
		typ := reflect.TypeOf(types[int(kind)%len(types)])
		dst := reflect.New(typ)
		req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
		req.Header.Set("Content-Type", ContentTypeJSON)
		var v problem.Validation
		var derr error
		Handle(slog.New(slog.DiscardHandler), func(r *http.Request) (any, error) {
			derr = DecodeJSON(r, dst.Interface().(request.Request), &v)
			return nil, nil
		})(httptest.NewRecorder(), req)

		if derr != nil {
			e := problemOf(t, derr)
			if len(body) > MaxBodyBytes {
				require.Equal(t, dto.TypeBodyTooLarge, e.Type)
			} else {
				require.Equal(t, dto.TypeInvalidBody, e.Type)
			}
			return
		}
		require.LessOrEqual(t, len(body), MaxBodyBytes)
		require.True(t, jsontext.Value(body).IsValid(), "only valid JSON is accepted")
		require.Equal(t, jsontext.Kind('{'), jsontext.Value(bytes.TrimSpace(body)).Kind(), "only an object is accepted")

		verr := v.Err()
		if verr == nil {
			requireInBounds(t, dst.Interface())
			return
		}
		e := problemOf(t, verr)
		require.Equal(t, dto.TypeValidation, e.Type)
		for _, it := range e.Errors {
			require.Empty(t, it.Param)
			require.True(t, pointerResolves(dst.Elem(), it.Pointer), "pointer %q names a member of the body", it.Pointer)
		}
	})
}

// pointerResolves reports whether the JSON Pointer p names a member present in v (a struct
// field by json key, set when it is a pointer, or a slice element in range).
func pointerResolves(v reflect.Value, p string) bool {
	if !strings.HasPrefix(p, "/") {
		return false
	}
	for seg := range strings.SplitSeq(p[1:], "/") {
		for v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return false
			}
			v = v.Elem()
		}
		switch v.Kind() {
		case reflect.Struct:
			f, ok := fieldByJSONKey(v, seg)
			if !ok {
				return false
			}
			v = f
		case reflect.Slice:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= v.Len() || strconv.Itoa(i) != seg {
				return false
			}
			v = v.Index(i)
		default:
			return false
		}
	}
	return !(v.Kind() == reflect.Pointer && v.IsNil())
}

func fieldByJSONKey(v reflect.Value, key string) (reflect.Value, bool) {
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if name == key {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// requireInBounds requires a request body with no recorded problem to be within its type's
// documented bounds (docs/api/openapi.yaml and the request package's constants).
func requireInBounds(t *testing.T, body any) {
	t.Helper()
	switch b := body.(type) {
	case *request.SourceAdd:
		require.True(t, b.URL != "" && len(b.URL) <= request.MaxSourceURLBytes && oracleText(b.URL, request.MaxSourceURLBytes))
		_, err := ao3url.Parse(b.URL)
		require.NoError(t, err)
		requireEnumOrEmpty(t, b.Order)
		requirePriority(t, b.Priority)
		requirePollInterval(t, b.PollInterval)
	case *request.SourceRefresh:
	case *request.SourceSettings:
		if b.Priority != nil {
			requirePriority(t, *b.Priority)
		}
		requirePollInterval(t, b.PollInterval)
	case *request.WorkQuery:
		if b.Q != "" {
			require.True(t, oracleText(b.Q, request.MaxQueryRunes))
			terms := searchquery.Terms(b.Q)
			require.LessOrEqual(t, len(terms), searchquery.MaxTerms)
			require.True(t, slices.ContainsFunc(terms, func(t searchquery.Term) bool { return !t.Exclude }))
		}
		requireIDs(t, b.TagIDs, request.MaxWorkQueryTags)
		requireIDs(t, b.ExcludeTagIDs, request.MaxWorkQueryTags)
		require.True(t, b.Language == "" || oracleText(b.Language, request.MaxLanguageRunes))
		requireDistinctEnums(t, b.Statuses)
		for _, r := range []request.IntRange{b.Words, b.Kudos, b.Hits, b.Comments, b.Bookmarks} {
			for _, n := range []*int{r.Min, r.Max} {
				require.True(t, n == nil || (*n >= 0 && *n <= request.MaxCount))
			}
			require.True(t, r.Min == nil || r.Max == nil || *r.Min <= *r.Max)
		}
		for _, r := range []request.DateRange{b.Revised, b.Published} {
			from, err := parseOptionalDate(r.From)
			require.NoError(t, err)
			to, err := parseOptionalDate(r.To)
			require.NoError(t, err)
			require.True(t, from.IsZero() || to.IsZero() || !from.After(to))
		}
		requireEnumOrEmpty(t, b.Sort)
		require.True(t, b.Sort != dto.WorkSortBestMatch || b.Q != "")
		requireEnumOrEmpty(t, b.Direction)
	case *request.TagQuery:
		requireNameWords(t, b.Name, request.MaxTagNameQueryRunes, request.MaxTagNameQueryWords)
		requireDistinctEnums(t, b.Types)
		requireIDs(t, b.ParentIDs, request.MaxTagQueryParents)
		requireEnumOrEmpty(t, b.Sort)
		requireEnumOrEmpty(t, b.Direction)
	case *request.CreatorQuery:
		requireNameWords(t, b.Name, request.MaxCreatorNameQueryRunes, request.MaxCreatorNameQueryWords)
		requireEnumOrEmpty(t, b.Sort)
		requireEnumOrEmpty(t, b.Direction)
	default:
		t.Fatalf("requireInBounds: no bounds for %T; add them", body)
	}
}

type enum interface {
	~string
	EnumValues() []string
}

func requireEnumOrEmpty[E enum](t *testing.T, e E) {
	t.Helper()
	require.True(t, e == "" || slices.Contains(e.EnumValues(), string(e)), "%T %q", e, e)
}

func requireDistinctEnums[E enum](t *testing.T, es []E) {
	t.Helper()
	for i, e := range es {
		require.Contains(t, e.EnumValues(), string(e))
		require.NotContains(t, es[:i], e)
	}
}

func requireIDs(t *testing.T, ids []int64, limit int) {
	t.Helper()
	require.LessOrEqual(t, len(ids), limit)
	for i, id := range ids {
		require.True(t, id >= 1 && id <= request.MaxID)
		require.NotContains(t, ids[:i], id)
	}
}

func requirePriority(t *testing.T, p int) {
	t.Helper()
	require.True(t, p >= -request.MaxSourcePriority && p <= request.MaxSourcePriority)
}

func requirePollInterval(t *testing.T, d dto.Duration) {
	t.Helper()
	p := d.Duration()
	require.True(t, p == 0 || (p >= request.MinPollInterval && p <= request.MaxPollInterval))
}

func requireNameWords(t *testing.T, name string, maxRunes, maxWords int) {
	t.Helper()
	if name == "" {
		return
	}
	n := len(strings.Fields(name))
	require.True(t, n >= 1 && n <= maxWords && oracleText(name, maxRunes))
}

func parseOptionalDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.DateOnly, s)
}
