package controllers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/imagefetch"
	"github.com/rmb938/personal-aO3-mirror/internal/model"
	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

const fixtureImageBase = "https://img.example.test/fixture/"

// imageEnv is a store with work 1 embedding three images, an image directory and controllers
// reading it:
//   - one.png is downloaded (its file is written);
//   - gone.png is downloaded but its file was removed;
//   - queued.png is not downloaded yet.
type imageEnv struct {
	st   *store.Store
	svc  *Controllers
	dir  string
	logs *bytes.Buffer
	// sha of each downloaded image by file name.
	sha map[string]string
}

func newImageEnv(t *testing.T) *imageEnv {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir(), store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	e := &imageEnv{st: st, dir: t.TempDir(), logs: &bytes.Buffer{}, sha: map[string]string{}}
	e.svc = New(Options{Store: st, ImageDir: e.dir, Logger: slog.New(slog.NewJSONHandler(e.logs, nil))})

	bodies := map[string][]byte{"one.png": []byte("fixture image bytes one"), "gone.png": []byte("fixture image bytes gone")}
	require.NoError(t, st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.UpsertWork(ctx, model.Work{ID: 1, Title: "Fixture Title", Revised: "2026-11-01"}, model.FromPage, 1); err != nil {
			return err
		}
		_, err := tx.QueueImages(ctx, 1, []string{fixtureImageBase + "one.png", fixtureImageBase + "gone.png", fixtureImageBase + "queued.png"})
		return err
	}))
	v, err := e.svc.GetWork(ctx, 1)
	require.NoError(t, err)
	require.Len(t, v.Images, 3)
	for i, name := range []string{"one.png", "gone.png"} {
		sum := sha256.Sum256(bodies[name])
		sha := hex.EncodeToString(sum[:])
		e.sha[name] = sha
		if name == "one.png" {
			p := imagefetch.Path(e.dir, sha, "image/png")
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
			require.NoError(t, os.WriteFile(p, bodies[name], 0o600))
		}
		require.NoError(t, st.InTx(ctx, func(ctx context.Context, tx *store.Tx) error {
			return tx.ImageDone(ctx, int64(i+1), sha, "image/png", len(bodies[name]))
		}))
	}
	return e
}

func TestGetImage(t *testing.T) {
	e := newImageEnv(t)
	ctx := context.Background()

	img, err := e.svc.GetImage(ctx, e.sha["one.png"])
	require.NoError(t, err)
	assert.Equal(t, Image{ContentType: "image/png", Body: []byte("fixture image bytes one")}, img)

	for name, sha := range map[string]string{
		"unknown":       strings.Repeat("c3", 32),
		"upper case":    strings.ToUpper(e.sha["one.png"]),
		"short":         e.sha["one.png"][:63],
		"path":          "../" + e.sha["one.png"][3:],
		"empty":         "",
		"not hex":       strings.Repeat("zz", 32),
		"file removed":  e.sha["gone.png"],
		"one char long": e.sha["one.png"] + "0",
	} {
		_, err := e.svc.GetImage(ctx, sha)
		require.ErrorIs(t, err, store.ErrNoImage, name)
	}
	assert.Contains(t, e.logs.String(), `"msg":"image file missing; reported as no image","image_id":2`)
	assert.NotContains(t, e.logs.String(), "fixture image bytes")
}

func TestGetImageRefusesMismatchedFile(t *testing.T) {
	e := newImageEnv(t)
	ctx := context.Background()
	p := imagefetch.Path(e.dir, e.sha["one.png"], "image/png")
	require.NoError(t, os.WriteFile(p, []byte("fixture image bytes changed"), 0o600))

	_, err := e.svc.GetImage(ctx, e.sha["one.png"])
	require.ErrorIs(t, err, errImageFile)
	require.NotErrorIs(t, err, store.ErrNoImage)
	assert.Contains(t, e.logs.String(), `"msg":"image file does not match its hash","image_id":1`)
	assert.NotContains(t, e.logs.String(), "fixture image bytes")
}

func TestGetImageRefusesOversizedFile(t *testing.T) {
	e := newImageEnv(t)
	p := imagefetch.Path(e.dir, e.sha["one.png"], "image/png")
	require.NoError(t, os.Truncate(p, MaxImageFileBytes+1))

	_, err := e.svc.GetImage(context.Background(), e.sha["one.png"])
	require.ErrorIs(t, err, errImageFile)
	assert.Contains(t, e.logs.String(), `"msg":"image file unreadable"`)
}

func TestGetWorkImages(t *testing.T) {
	e := newImageEnv(t)
	v, err := e.svc.GetWork(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, []store.WorkImage{
		{URL: fixtureImageBase + "one.png", Status: store.ImageOK, SHA256: e.sha["one.png"]},
		{URL: fixtureImageBase + "gone.png", Status: store.ImageOK, SHA256: e.sha["gone.png"]},
		{URL: fixtureImageBase + "queued.png", Status: store.ImageQueued},
	}, v.Images)
}

func TestValidSHA256(t *testing.T) {
	assert.True(t, ValidSHA256(strings.Repeat("0123456789abcdef", 4)))
	for _, s := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64)} {
		assert.False(t, ValidSHA256(s), "%q", s)
	}
}
