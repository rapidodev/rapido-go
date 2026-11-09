package nodecore

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync/atomic"
	"time"

	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Why an outbound cannot be tested at all. Callers tell them apart with
// errors.Is; any other error from URLTest is a test that ran and failed.
var (
	ErrNoOutbound    = errors.New("no outbound with this tag is running")
	ErrBlockOutbound = errors.New("a block outbound refuses every connection")
	ErrNoTCPOutbound = errors.New("this outbound carries no TCP")
)

// URLTestResult is one measurement through an outbound.
type URLTestResult struct {
	// Delay runs from the dial to the response headers: connecting through
	// the outbound, the TLS handshake with the test server and one request.
	// An outbound that puts off its own handshake until the first write is
	// timed from that write, as sing-box's urltest does.
	Delay      time.Duration
	StatusCode int
}

func (n *Node) testableOutbound(tag string) (adapter.Outbound, error) {
	out, ok := n.box.Outbound().Outbound(tag)
	if !ok {
		return nil, ErrNoOutbound
	}
	if out.Type() == C.TypeBlock {
		return nil, ErrBlockOutbound
	}
	if !slices.Contains(out.Network(), N.NetworkTCP) {
		return nil, ErrNoTCPOutbound
	}
	return out, nil
}

// CanURLTest reports, without dialing, why URLTest would refuse tag, or nil.
func (n *Node) CanURLTest(tag string) error {
	_, err := n.testableOutbound(tag)
	return err
}

// URLTest makes one HEAD request to link through the outbound tagged tag and
// times it. It measures what sing-box's urltest group does
// (common/urltest.URLTest), but keeps the status code that helper drops. The
// outbound is dialed directly, not through the router, so routing rules play
// no part: the result is the outbound's own. ctx bounds the whole test.
func (n *Node) URLTest(ctx context.Context, tag, link string) (URLTestResult, error) {
	out, err := n.testableOutbound(tag)
	if err != nil {
		return URLTestResult{}, err
	}
	target, err := url.Parse(link)
	if err != nil {
		return URLTestResult{}, err
	}
	port := target.Port()
	if port == "" {
		switch target.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return URLTestResult{}, fmt.Errorf("nodecore: test URL must be http or https, not %q", target.Scheme)
		}
	}

	start := time.Now()
	conn, err := out.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddrHostPortStr(target.Hostname(), port))
	if err != nil {
		return URLTestResult{}, err
	}
	defer conn.Close()
	if N.NeedHandshakeForWrite(conn) {
		start = time.Now()
	}

	var dialed atomic.Bool
	transport := &http.Transport{
		// The one connection already made through the outbound, never a second.
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if dialed.Swap(true) {
				return nil, errors.New("nodecore: the test connection was already used")
			}
			return conn, nil
		},
		TLSClientConfig:   &tls.Config{},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, link, nil)
	if err != nil {
		return URLTestResult{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return URLTestResult{}, err
	}
	resp.Body.Close()
	return URLTestResult{Delay: time.Since(start), StatusCode: resp.StatusCode}, nil
}
