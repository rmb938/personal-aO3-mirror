// Package jobs holds job kinds, priority classes and dedupe keys shared by the scheduler and
// the sources service.
package jobs

import "strconv"

// Priority classes (lower runs first).
const (
	ClassOnDemand    = 1 // single works requested by the user
	ClassPoll        = 2 // update polls (newest revised first)
	ClassPollFetch   = 3 // work fetches discovered by polls
	ClassBackfill    = 4 // backfill listing pages and their work fetches
	ClassTagInfo     = 5 // tag wrangling info
	ClassMaintenance = 6 // rescans, deletion checks
)

// Job kinds.
const (
	KindPoll     = "poll"
	KindBackfill = "backfill"
	KindWork     = "work"
	KindTagInfo  = "tag_info"
	KindSeries   = "series_info"
	// KindSourceAdd fetches page 1 of a source added from the CLI and creates it (target: the
	// source_adds row id).
	KindSourceAdd = "source_add"
)

// SourceAddKey is the dedupe key for adding one source; hash is a content-free hash of its
// kind, target and filters.
func SourceAddKey(hash string) string { return "source_add:" + hash }

// TagKey is the dedupe key for fetching a tag's info page.
func TagKey(id int64) string { return "tag:" + strconv.FormatInt(id, 10) }

// WorkKey is the dedupe key for fetching a work (one active fetch per work).
func WorkKey(id int64) string { return "work:" + strconv.FormatInt(id, 10) }

// SeriesKey is the dedupe key for fetching a series page.
func SeriesKey(id int64) string { return "series:" + strconv.FormatInt(id, 10) }

// PollKey is the dedupe key for one page of a source's update poll.
func PollKey(sourceID int64, page int) string {
	return "poll:" + strconv.FormatInt(sourceID, 10) + ":" + strconv.Itoa(page)
}

// BackfillKey is the dedupe key for one backfill page of a source; windowTo ("" when not
// windowed) keeps pages of different date windows apart.
func BackfillKey(sourceID int64, windowTo string, page int) string {
	return "backfill:" + strconv.FormatInt(sourceID, 10) + ":" + windowTo + ":" + strconv.Itoa(page)
}
