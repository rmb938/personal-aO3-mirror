package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

func TestSourcesLifecycle(t *testing.T) {
	s, _, _ := openTest(t)
	ctx := context.Background()
	row := SourceRow{Kind: "tag", Target: "Fixture Fandom", FiltersJSON: "{}", Path: "/tags/Fixture%20Fandom/works",
		State: SourceActive, BackfillOrder: "kudos_desc", PollInterval: 6 * time.Hour, TotalWorks: 3, EstRequests: 4, BackfillCursor: "{}"}
	id, err := s.InsertSource(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertSource(ctx, row); !errors.Is(err, ErrSourceExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if found, _ := s.FindSource(ctx, "tag", "Fixture Fandom", "{}"); found != id {
		t.Fatalf("find = %d", found)
	}

	s.UpsertWork(ctx, fixtureWork(1), model.FromBlurb, 1)
	s.UpsertWork(ctx, fixtureWork(2), model.FromPage, 1)
	if err := s.LinkSourceWorks(ctx, id, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSource(ctx, id)
	if err != nil || got.MemberWorks != 2 || got.FetchedWorks != 1 || got.PollInterval != 6*time.Hour {
		t.Fatalf("get: %+v %v", got, err)
	}

	// Jobs: queued ones get cancelled, running ones are left alone.
	now := time.Now().Unix()
	for _, st := range []string{"queued", "queued", "running"} {
		if _, err := s.Meta.Exec(`INSERT INTO jobs (kind, source_id, target, priority_class, state, dedupe_key, created_at, updated_at)
			VALUES ('listing_page', ?, '/x', 4, ?, ?, ?, ?)`, id, st, st+time.Now().String(), now, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetSourceState(ctx, id, SourcePaused, SourceActive); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSourceState(ctx, id, SourceActive, SourcePendingConfirm); err == nil || !strings.Contains(err.Error(), "is paused") {
		t.Fatalf("bad transition should fail: %v", err)
	}
	n, err := s.CancelSourceJobs(ctx, id)
	if err != nil || n != 2 {
		t.Fatalf("cancelled %d %v", n, err)
	}
	if err := s.DeleteSource(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSource(ctx, id); !errors.Is(err, ErrNoSource) {
		t.Fatalf("after delete: %v", err)
	}
	var works, members int
	s.Meta.QueryRow(`SELECT COUNT(*) FROM works`).Scan(&works)
	s.Meta.QueryRow(`SELECT COUNT(*) FROM source_works`).Scan(&members)
	if works != 2 || members != 0 {
		t.Fatalf("works must be kept, memberships removed: works=%d members=%d", works, members)
	}
}
