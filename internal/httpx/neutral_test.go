package httpx

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rmb938/personal-aO3-mirror/internal/fakeao3"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// neutralClient is a client against srv whose transport is rt (robots.txt still goes to srv).
func neutralClient(t *testing.T, srv *fakeao3.Server, clk *fakeClock, maxBody int64, rt roundTripFunc) *Client {
	t.Helper()
	base := http.DefaultTransport
	c, err := New(Options{BaseURL: srv.URL, UserAgent: testUA, Interval: 10 * time.Second, Clock: clk,
		Rand: rand.NewPCG(1, 1), Logger: slog.New(slog.DiscardHandler), MaxBody: maxBody,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path == "/robots.txt" || rt == nil {
				return base.RoundTrip(r)
			}
			return rt(r)
		})})
	require.NoError(t, err)
	return c
}

// Cancelling a request (shutdown, Ctrl-C) is not an AO3 failure: no backoff, no breaker count.
func TestCancelledRequestIsNotAFailure(t *testing.T) {
	srv := fakeao3.New()
	t.Cleanup(srv.Close)
	clk := newFakeClock()
	cancels := make(chan context.CancelFunc, 1)
	c := neutralClient(t, srv, clk, 0, func(r *http.Request) (*http.Response, error) {
		(<-cancels)() // the caller cancels mid-request
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	for range breakerThreshold + 1 {
		ctx, cancel := context.WithCancel(context.Background())
		cancels <- cancel
		_, err := c.Get(ctx, KindWork, "/works/1")
		cancel()
		require.ErrorIs(t, err, context.Canceled)
	}
	st := c.PacerState()
	require.Equal(t, 0, st.ConsecFails, "cancellations counted as failures")
	require.False(t, st.BreakerOpen)
	require.False(t, st.BackoffTill.After(clk.Now()), "cancellation set a backoff")
}

// An oversized body is refused without backoff; a declared Content-Length over the limit is not
// downloaded at all.
func TestTooLargeIsNotAFailure(t *testing.T) {
	srv := fakeao3.New()
	t.Cleanup(srv.Close)
	big := fakeao3.WorkPage(strings.Repeat("x", 4096))
	srv.Handle("/works/1", fakeao3.Reply{Body: big})
	clk := newFakeClock()
	c := neutralClient(t, srv, clk, 1024, nil)
	for range breakerThreshold + 1 {
		_, err := c.Get(context.Background(), KindWork, "/works/1")
		require.ErrorIs(t, err, ErrTooLarge)
	}
	st := c.PacerState()
	require.Equal(t, 0, st.ConsecFails, "oversized body counted as an AO3 failure")
	require.False(t, st.BackoffTill.After(clk.Now()), "oversized body set a backoff")

	read := false
	c = neutralClient(t, srv, clk, 1024, func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, ContentLength: 1 << 30, Header: http.Header{}, Request: r,
			Body: readFlag{&read}}, nil
	})
	_, err := c.Get(context.Background(), KindWork, "/works/2")
	require.ErrorIs(t, err, ErrTooLarge)
	require.False(t, read, "body read despite Content-Length over the limit")
}

type readFlag struct{ read *bool }

func (f readFlag) Read([]byte) (int, error) { *f.read = true; return 0, nil }
func (f readFlag) Close() error             { return nil }
