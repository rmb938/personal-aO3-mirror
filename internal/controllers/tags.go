package controllers

import (
	"context"
	"strconv"

	"github.com/rmb938/personal-aO3-mirror/internal/ao3url"
	"github.com/rmb938/personal-aO3-mirror/internal/httpx"
	"github.com/rmb938/personal-aO3-mirror/internal/parse"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

// FetchTagInfo fetches /tags/<tag> and records canonical/synonym/parent/meta relations.
func (c *Controllers) FetchTagInfo(ctx context.Context, id int64) (Detail, error) {
	t, err := c.store.TagByID(ctx, id)
	if err != nil {
		return nil, PermanentError{Code: CodeNoTag}
	}
	resp, err := c.get(ctx, httpx.KindTag, "/tags/"+ao3url.TagParam(t.Name))
	if err != nil {
		return nil, err
	}
	skelID := strconv.FormatInt(id, 10)
	detail := Detail{"tag_id": id, "class": string(resp.Class), "req_id": resp.ReqID}
	if resp.Class == httpx.ClassNotFound {
		return detail, c.store.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true})
	}
	if err := outcome(resp); err != nil {
		if resp.Class == httpx.ClassUnexpected {
			c.saveSkeleton(ctx, "tag", skelID, resp.Body)
			// Mark attempted so the next try waits for tags.info_ttl instead of looping.
			if uerr := c.store.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true}); uerr != nil {
				return detail, uerr
			}
		}
		return detail, err
	}
	info, rep, err := parse.ParseTag(resp.Body)
	c.logger(ctx).Debug("parse report", append([]any{"req_id", resp.ReqID, "tag_id", id}, rep.Attrs()...)...)
	if err != nil {
		c.saveSkeleton(ctx, "tag", skelID, resp.Body)
		if uerr := c.store.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Missing: true}); uerr != nil {
			return detail, uerr
		}
		return detail, parseUnexpected(rep.Missing)
	}
	if err := c.store.UpdateTagInfo(ctx, id, store.TagInfoUpdate{Type: info.Type, Canonical: info.Canonical,
		MergerOf: info.MergerOf, Parents: info.Parents, MetaTags: info.MetaTags, Synonyms: info.Synonyms}); err != nil {
		return detail, err
	}
	detail["type"], detail["canonical"], detail["parents"] = string(info.Type), info.Canonical, len(info.Parents)
	return detail, nil
}
