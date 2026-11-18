package httpx

import (
	"crypto/tls"
	"net"
	"net/http"
	"syscall"
	"time"
)

// ExternalOptions configure the client for non-AO3 hosts (images embedded in works).
type ExternalOptions struct {
	UserAgent string
	// DialControl vets every address before connecting (the image worker refuses private and
	// loopback addresses: fanwork HTML is untrusted input). Required.
	DialControl func(network, address string, c syscall.RawConn) error
	Timeout     time.Duration // whole request, body included (default 2m)
}

// NewExternalClient returns the http.Client for non-AO3 hosts. Like the AO3 client it is built
// here so every request carries the contact User-Agent. It connects directly (no proxy, so
// DialControl sees the real target) and never follows redirects: the caller checks AO3,
// robots.txt and per-host pacing for each hop itself.
func NewExternalClient(o ExternalOptions) (*http.Client, error) {
	if o.DialControl == nil {
		return nil, errNoDialControl
	}
	if o.Timeout <= 0 {
		o.Timeout = 2 * time.Minute
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, Control: o.DialControl}
	rt, err := newUATransport(o.UserAgent, &http.Transport{
		Proxy:                 nil,
		DialContext:           dialer.DialContext,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConnsPerHost:   1,
	})
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, nil
}
