package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/model"
)

// Fixture image hashes: 64 hex characters, like imagefetch's.
var (
	fixtureSHA1 = strings.Repeat("a1", 32)
	fixtureSHA2 = strings.Repeat("b2", 32)
)

// seedImages queues four images for work 1 and one for work 2:
//   - one.png, ok with fixtureSHA1 as image/png;
//   - two.png, later ok with the same bytes (fixtureSHA1) but served as image/gif;
//   - three.png, failed;
//   - four.png, still queued;
//   - five.png (work 2), ok with fixtureSHA2.
//
// It returns the image ids by file name.
func seedImages(t *testing.T) (*testStore, map[string]int64) {
	t.Helper()
	s, _, _ := openTest(t)
	ctx := context.Background()
	for _, id := range []int64{1, 2} {
		_, err := s.UpsertWork(ctx, fixtureWork(id), model.FromPage, 1)
		require.NoError(t, err)
	}
	const base = "https://img.example.test/fixture/"
	_, err := s.QueueImages(ctx, 1, []string{base + "one.png", base + "two.png", base + "three.png", base + "four.png"})
	require.NoError(t, err)
	_, err = s.QueueImages(ctx, 2, []string{base + "five.png"})
	require.NoError(t, err)
	ids := map[string]int64{}
	rows, err := s.meta.QueryContext(ctx, `SELECT id, url FROM images`)
	require.NoError(t, err)
	defer func() { require.NoError(t, rows.Close()) }()
	for rows.Next() {
		var id int64
		var u string
		require.NoError(t, rows.Scan(&id, &u))
		ids[strings.TrimPrefix(u, base)] = id
	}
	require.NoError(t, rows.Err())
	require.Len(t, ids, 5)
	require.NoError(t, s.ImageDone(ctx, ids["one.png"], fixtureSHA1, "image/png", 10))
	require.NoError(t, s.ImageDone(ctx, ids["two.png"], fixtureSHA1, "image/gif", 10))
	require.NoError(t, s.ImageFinal(ctx, ids["three.png"], ImageFailed, "status=404"))
	require.NoError(t, s.ImageDone(ctx, ids["five.png"], fixtureSHA2, "image/webp", 20))
	return s, ids
}

func TestImageBySHA(t *testing.T) {
	s, ids := seedImages(t)
	ctx := context.Background()

	img, err := s.ImageBySHA(ctx, fixtureSHA1)
	require.NoError(t, err)
	require.Equal(t, StoredImage{ID: ids["one.png"], SHA256: fixtureSHA1, ContentType: "image/png", Bytes: 10}, img,
		"two rows share the hash: the first stored wins")

	img, err = s.ImageBySHA(ctx, fixtureSHA2)
	require.NoError(t, err)
	require.Equal(t, StoredImage{ID: ids["five.png"], SHA256: fixtureSHA2, ContentType: "image/webp", Bytes: 20}, img)

	for _, sha := range []string{strings.Repeat("c3", 32), "", strings.ToUpper(fixtureSHA1)} {
		_, err = s.ImageBySHA(ctx, sha)
		require.ErrorIs(t, err, ErrNoImage, "%q", sha)
	}
}

func TestImageBySHAOnlyOK(t *testing.T) {
	s, ids := seedImages(t)
	ctx := context.Background()
	// An image whose row left ok keeps its hash column; it is no longer served.
	_, err := s.meta.ExecContext(ctx, `UPDATE images SET status = 'failed' WHERE id = ?`, ids["five.png"])
	require.NoError(t, err)
	_, err = s.ImageBySHA(ctx, fixtureSHA2)
	require.ErrorIs(t, err, ErrNoImage)
}

func TestWorkImages(t *testing.T) {
	s, _ := seedImages(t)
	ctx := context.Background()
	const base = "https://img.example.test/fixture/"

	got, err := s.WorkImages(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []WorkImage{
		{URL: base + "one.png", Status: ImageOK, SHA256: fixtureSHA1},
		{URL: base + "two.png", Status: ImageOK, SHA256: fixtureSHA1},
		{URL: base + "three.png", Status: ImageFailed},
		{URL: base + "four.png", Status: ImageQueued},
	}, got, "first seen first; a hash only once downloaded")

	got, err = s.WorkImages(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []WorkImage{{URL: base + "five.png", Status: ImageOK, SHA256: fixtureSHA2}}, got)

	got, err = s.WorkImages(ctx, 999)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestImagePlans(t *testing.T) {
	s, _, _ := openTest(t)
	plan := explain(t, s, imageBySHA, fixtureSHA1)
	require.Contains(t, plan, "INDEX images_sha256 (sha256=?)", plan)
	require.NotContains(t, plan, "SCAN images", plan)

	plan = explain(t, s, workImages, 1)
	require.Contains(t, plan, "sqlite_autoindex_work_images_1 (work_id=?)", plan)
	require.Contains(t, plan, "USING INTEGER PRIMARY KEY (rowid=?)", plan)
	require.NotContains(t, plan, "SCAN i", plan)
}
