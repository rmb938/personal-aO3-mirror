package convert

import (
	"slices"
	"strconv"
	"strings"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/controllers"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/dto"
)

// SourceList is one page of sources.
func SourceList(items []model.Source, total int, page, perPage int64) dto.List[dto.Source] {
	return list(items, Source, total, page, perPage)
}

// Source maps one source. An unreadable filters or cursor column is left out (filters []),
// not an error: the scheduler reports it on the source (bad_filters, bad_cursor).
func Source(s model.Source) dto.Source {
	out := dto.Source{
		ID:             s.ID,
		Kind:           dto.SourceKind(s.Kind),
		Target:         s.Target,
		Filters:        filters(s.FiltersJSON),
		Path:           s.Path,
		State:          dto.SourceState(s.State),
		Order:          dto.Order(s.BackfillOrder),
		PollInterval:   dto.Duration(s.PollInterval),
		Priority:       s.Priority,
		TotalWorks:     known(s.TotalWorks),
		EstRequests:    known(s.EstRequests),
		MemberWorks:    s.MemberWorks,
		FetchedWorks:   s.FetchedWorks,
		Backfill:       backfill(s.BackfillCursor),
		CreatedAt:      utc(s.CreatedAt),
		UpdatedAt:      utc(s.UpdatedAt),
		LastPollAt:     utc(s.LastPollAt),
		NextPollAt:     utc(s.NextPollAt),
		LastFullScanAt: utc(s.LastFullScanAt),
		BackfillDoneAt: utc(s.BackfillDoneAt),
		LastError:      s.LastError,
	}
	if k := ao3url.Kind(s.Kind); k == ao3url.KindSeries || k == ao3url.KindWork {
		if id, err := strconv.ParseInt(s.Target, 10, 64); err == nil && id > 0 {
			out.TargetID = id
		}
	}
	return out
}

// filters maps the stored name → values JSON to a list sorted by name (nil when none).
func filters(js string) []dto.Filter {
	vals, err := ao3url.ParseFiltersJSON(js)
	if err != nil {
		return nil
	}
	var out []dto.Filter
	for name, vs := range vals {
		out = append(out, dto.Filter{Name: name, Values: slices.Clone(vs)})
	}
	slices.SortFunc(out, func(a, b dto.Filter) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// backfill maps the stored cursor; nil when there is none (or it is unreadable).
func backfill(js string) *dto.Backfill {
	c, err := controllers.ParseSourceCursor(js)
	if err != nil || c == (controllers.SourceCursor{}) {
		return nil
	}
	return &dto.Backfill{
		NextPage:        c.NextPage,
		Order:           dto.Order(c.Order),
		WindowStartOn:   c.WindowFrom,
		WindowEndOn:     c.WindowTo,
		Rescan:          c.Rescan,
		RescanStartedAt: unix(c.RescanStarted),
		BlockedParser:   c.BlockedParser,
		RetryAt:         unix(c.RetryAt),
	}
}
