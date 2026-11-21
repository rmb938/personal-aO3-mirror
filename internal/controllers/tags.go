package controllers

import (
	"context"
	"errors"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// FetchTagInfo fetches /tags/<tag> and records canonical/synonym/parent/meta relations.
// Steps: read the tag, fetch its page, then one write transaction for whatever the page said.
// final says this is the job's last attempt.
func (c *Controllers) FetchTagInfo(ctx context.Context, id int64, final bool) (*TagInfoDetail, error) {
	t, err := store.ReadValue(ctx, c.store, func(ctx context.Context, tx *store.ReadTx) (store.TagRef, error) {
		return tx.TagByID(ctx, id)
	})
	if errors.Is(err, store.ErrNoTag) {
		return nil, PermanentError{Code: CodeNoTag}
	}
	if err != nil {
		return nil, err
	}
	update := func(u store.TagInfoUpdate) error {
		return c.store.InTx(ctx, func(ctx context.Context, tx *store.Tx) error { return tx.UpdateTagInfo(ctx, id, u) })
	}
	// attempted marks a fetch that yielded nothing, so the tag waits for tags.info_ttl.
	attempted := func() error { return update(store.TagInfoUpdate{AttemptOnly: true}) }
	resp, err := c.ao3Get(ctx, httpx.KindTag, "/tags/"+ao3url.TagParam(t.Name))
	if err != nil {
		return nil, markIfGivingUp(ctx, err, final, attempted)
	}
	skelID := strconv.FormatInt(id, 10)
	detail := &TagInfoDetail{TagID: new(id), Class: new(string(resp.Class)), ReqID: new(resp.ReqID)}
	if resp.Class == httpx.ClassNotFound {
		return detail, attempted()
	}
	if err := outcome(resp); err != nil {
		// Permanent (restricted, hidden, odd answer) or out of retries. An odd answer is not a
		// tag page, so no skeleton is saved for it.
		return detail, markIfGivingUp(ctx, err, final, attempted)
	}
	info, rep, err := parse.ParseTag(resp.Body)
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "tag_id", id}, rep.Attrs()...)...)
	if err != nil {
		c.saveSkeleton(ctx, "tag", skelID, resp.Body)
		if uerr := attempted(); uerr != nil {
			return detail, uerr
		}
		return detail, parseUnexpected(rep.Missing)
	}
	if err := update(store.TagInfoUpdate{Type: info.Type, Canonical: info.Canonical, MergerOf: info.MergerOf,
		Parents: info.Parents, MetaTags: info.MetaTags, Synonyms: info.Synonyms}); err != nil {
		return detail, err
	}
	detail.Type, detail.Canonical, detail.Parents = new(string(info.Type)), new(info.Canonical), new(len(info.Parents))
	return detail, nil
}
