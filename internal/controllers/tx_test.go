package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// hookFetcher runs during (once) before delegating the next listing request, simulating a CLI
// command that changes the source while the daemon's fetch is in flight.
type hookFetcher struct {
	inner  Fetcher
	during func()
}

func (h *hookFetcher) Get(ctx context.Context, kind httpx.Kind, ref string) (*httpx.Response, error) {
	if h.during != nil && strings.HasPrefix(ref, "/tags/") {
		during := h.during
		h.during = nil // once
		during()
	}
	return h.inner.Get(ctx, kind, ref)
}

// withHook returns controllers like e.svc whose next listing fetch first runs during.
func (e *env) withHook(during func()) *Controllers {
	o := e.opts
	o.Client = &hookFetcher{inner: e.opts.Client, during: during}
	return New(o)
}

func TestPollKeepsChangesMadeDuringTheFetch(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.AddSource(ctx, tagURL, AddSourceOptions{})
	require.NoError(t, err)
	id := res.Source.ID

	var want store.SourceRow
	ctl := e.withHook(func() {
		// `source set --poll 30m --priority 7` while the poll page is being fetched.
		prio := 7
		require.NoError(t, e.svc.SetSourceSettings(ctx, id, 30*time.Minute, &prio))
		var err error
		want, err = e.svc.GetSource(ctx, id)
		require.NoError(t, err)
	})
	_, err = ctl.PollSourcePage(ctx, id, 1)
	require.NoError(t, err)

	got, err := e.svc.GetSource(ctx, id)
	require.NoError(t, err)
	require.Equal(t, 30*time.Minute, got.PollInterval)
	require.Equal(t, 7, got.Priority)
	require.Equal(t, want.NextPollAt, got.NextPollAt, "poll overwrote the next-poll time set during its fetch")
}

func TestBackfillDoesNotAdvanceACursorMovedDuringTheFetch(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	res, err := e.svc.AddSource(ctx, tagURL, AddSourceOptions{})
	require.NoError(t, err)
	id := res.Source.ID
	src, err := e.svc.GetSource(ctx, id)
	require.NoError(t, err)
	cur, err := ParseSourceCursor(src.BackfillCursor)
	require.NoError(t, err)
	require.Equal(t, 2, cur.NextPage)

	moved := SourceCursor{NextPage: 1, Order: cur.Order, Rescan: true, RescanStarted: 1}.JSON()
	ctl := e.withHook(func() {
		// Something else (e.g. a refresh starting a rescan) resets the cursor mid-fetch.
		err := e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			r, err := tx.GetSource(ctx, id)
			if err != nil {
				return err
			}
			r.BackfillCursor = moved
			return tx.UpdateSourceProgress(ctx, r)
		})
		require.NoError(t, err)
	})
	detail, err := ctl.BackfillSourcePage(ctx, id, 2)
	require.NoError(t, err)
	require.Equal(t, true, detail["stale_after_fetch"])

	got, err := e.svc.GetSource(ctx, id)
	require.NoError(t, err)
	require.JSONEq(t, moved, got.BackfillCursor, "backfill overwrote a cursor moved during its fetch")
}

// An exported controller opens its own transaction, so calling one inside a transaction is
// refused (call the tx-taking helper instead).
func TestExportedControllerInsideTransactionIsRefused(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	res, err := e.svc.AddSource(ctx, "/works/1001", AddSourceOptions{})
	require.NoError(t, err)
	err = e.st.InTx(ctx, func(ctx context.Context, _ *store.Tx) error {
		return e.svc.PauseSource(ctx, res.Source.ID)
	})
	require.ErrorIs(t, err, store.ErrTxMisuse)
	require.ErrorContains(t, err, "nested")
}

func TestFetchInsideTransactionIsRefused(t *testing.T) {
	e := newEnv(t, 5000)
	before := len(e.srv.Requests())
	err := e.st.InTx(context.Background(), func(ctx context.Context, _ *store.Tx) error {
		_, err := e.svc.ao3Get(ctx, httpx.KindWork, "/works/1")
		return err
	})
	require.ErrorIs(t, err, store.ErrTxMisuse)
	require.Len(t, e.srv.Requests(), before, "the refused fetch must not reach AO3")
}

func TestAddIsAtomic(t *testing.T) {
	e := newEnv(t, 5000)
	ctx := context.Background()
	e.srv.Handle(tagURL, fakeao3.Reply{Body: fixture(t, "listing_tag.html")})
	ctl := e.withHook(func() {
		// The same source is added by someone else while page 1 is fetched: our insert fails.
		err := e.st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.InsertSource(ctx, store.SourceRow{Kind: "tag", Target: "Fixture Fandom", FiltersJSON: "{}",
				Path: tagURL, State: store.SourceActive, BackfillOrder: "kudos_desc", PollInterval: time.Hour})
			return err
		})
		require.NoError(t, err)
	})
	_, err := ctl.AddSource(ctx, tagURL, AddSourceOptions{})
	var ex ExistsError
	require.ErrorAs(t, err, &ex)
	counts, err := store.ReadValue(ctx, e.st, func(ctx context.Context, tx *store.ReadTx) (store.Counts, error) {
		return tx.Counts(ctx)
	})
	require.NoError(t, err)
	require.Zero(t, counts.WorksByStatus["ok"], "a failed add must not leave its page-1 works behind")
}
