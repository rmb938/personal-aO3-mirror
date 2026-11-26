package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/e2eseed"
	"github.com/rmb938/personal-aO3-mirror/internal/web/api/runtime"
)

// TestUIServer starts `dev ui-server`, signs in as the seeded account (with a recovery code, so
// the test never waits for a fresh TOTP step), reads a seeded work revealed, and checks that
// closing stdin stops the server and deletes its data dir.
func TestUIServer(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp) // os.MkdirTemp puts the data dir here
	stdin, stdinW := io.Pipe()
	stdoutR, stdout := io.Pipe()
	var stderr bytes.Buffer
	exit := make(chan int, 1)
	go func() {
		exit <- runUIServer(nil, stdin, stdout, &stderr)
		_ = stdout.Close()
	}()

	line, err := bufio.NewReader(stdoutR).ReadBytes('\n')
	require.NoError(t, err, "stderr: %s", stderr.String())
	var info uiServerInfo
	require.NoError(t, json.Unmarshal(line, &info))
	go func() { _, _ = io.Copy(io.Discard, stdoutR) }()
	acc := info.Accounts["reader"]
	assert.Equal(t, e2eseed.Username("reader"), acc.Username)
	assert.NotEmpty(t, acc.TOTPSecret)
	require.NotEmpty(t, acc.RecoveryCodes)
	assert.Contains(t, info.Works, e2eseed.WorkChaptered)

	post := func(path string, body any) map[string]any {
		t.Helper()
		b, err := json.Marshal(body)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, info.URL+path, bytes.NewReader(b))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, path)
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}
	challenge := post("/api/v1/auth/login", map[string]string{"username": acc.Username,
		"password": acc.Password})
	tokens := post("/api/v1/auth/login/totp", map[string]any{"challenge_token": challenge["challenge_token"],
		"recovery_code": acc.RecoveryCodes[0]})

	url := info.URL + "/api/v1/works/" + strconv.FormatInt(e2eseed.WorkChaptered, 10) + "?" + runtime.RevealParam + "=1"
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tokens["access_token"].(string))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s", body)
	assert.Contains(t, string(body), "Fixture Work Chaptered", "revealed")

	require.NoError(t, stdinW.Close())
	select {
	case code := <-exit:
		require.Equal(t, 0, code, "stderr: %s", stderr.String())
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("ui-server did not stop when stdin closed")
	}
	left, err := os.ReadDir(tmp)
	require.NoError(t, err)
	assert.Empty(t, left, "data dir deleted")
}

func TestUIServerRefusesNonLoopback(t *testing.T) {
	var stderr bytes.Buffer
	code := runUIServer([]string{"--addr", "0.0.0.0:0"}, bytes.NewReader(nil), io.Discard, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "loopback")
}
