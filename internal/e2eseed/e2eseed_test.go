package e2eseed

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/store"
)

func testDirs(t *testing.T) Dirs {
	t.Helper()
	root := t.TempDir()
	return Dirs{Data: filepath.Join(root, "data"), Images: filepath.Join(root, "data", "images"),
		Accounts: filepath.Join(root, "data", "api")}
}

func TestSeed(t *testing.T) {
	ctx := context.Background()
	dirs := testDirs(t)
	seeded, err := Seed(ctx, dirs, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	assert.Equal(t, Username, seeded.Account.Username)
	assert.Len(t, seeded.Works, 4)

	st, err := store.Open(ctx, dirs.Data, store.Options{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.InReadTx(ctx, func(ctx context.Context, tx *store.ReadTx) error {
		chs, err := tx.WorkChapters(ctx, WorkChaptered)
		require.NoError(t, err)
		assert.Len(t, chs, 3)
		imgs, err := tx.WorkImages(ctx, WorkChaptered)
		require.NoError(t, err)
		statuses := map[string]int{}
		for _, img := range imgs {
			statuses[img.Status]++
		}
		assert.Equal(t, map[string]int{store.ImageOK: 2, store.ImageQueued: 1}, statuses)
		return nil
	}))
}

// TestSeedRefusesUsedDir: Seed never writes into a directory that holds anything.
func TestSeedRefusesUsedDir(t *testing.T) {
	dirs := testDirs(t)
	require.NoError(t, os.MkdirAll(dirs.Data, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dirs.Data, "existing"), nil, 0o600))
	_, err := Seed(context.Background(), dirs, slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, "not empty")
}
