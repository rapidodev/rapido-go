// Package nodecontrol is the panel's client for a node agent's control plane:
// HTTPS on the node's API port with mutual TLS. The panel presents the Rapido
// CA certificate, the one identity a node accepts there (cmd/node's
// controlTLSConfig), and trusts a node only for a certificate that CA issued.
//
// Nodes otherwise poll the panel. This is for what only a node can answer on
// request, such as how its outbounds perform right now.
package nodecontrol

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	dialTimeout = 5 * time.Second
	maxResponse = 1 << 20
)

// Client calls node control planes. It keeps connections to nodes open between
// calls, so one is meant to be shared.
type Client struct {
	http      *http.Client
	transport *http.Transport
}

// New builds a client that authenticates as the panel with the CA's own
// certificate and key.
func New(caCertPEM, caKeyPEM string) (*Client, error) {
	identity, err := tls.X509KeyPair([]byte(caCertPEM), []byte(caKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("nodecontrol: load the panel identity: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(caCertPEM)) {
		return nil, errors.New("nodecontrol: the CA certificate is not valid PEM")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{identity},
			MinVersion:   tls.VersionTLS12,
			// A node's certificate names the node, not its address, so the usual
			// host name check cannot apply. VerifyConnection checks the chain to
			// the CA instead, on every connection including resumed ones.
			InsecureSkipVerify: true,
			VerifyConnection:   func(cs tls.ConnectionState) error { return verifyNode(cs, roots) },
		},
		DialContext:         (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: dialTimeout,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{http: &http.Client{Transport: transport}, transport: transport}, nil
}

func verifyNode(cs tls.ConnectionState, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("nodecontrol: the node sent no certificate")
	}
	intermediates := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		intermediates.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err != nil {
		return fmt.Errorf("nodecontrol: the node's certificate was not issued by this panel: %w", err)
	}
	return nil
}

// CloseIdleConnections closes the connections kept open to nodes.
func (c *Client) CloseIdleConnections() { c.transport.CloseIdleConnections() }

// StatusError is a node's answer other than 200.
type StatusError struct {
	Code   int
	Detail string
}

func (e *StatusError) Error() string {
	if e.Code == http.StatusNotFound || e.Code == http.StatusMethodNotAllowed {
		return "the node agent is too old for this; update it"
	}
	if e.Detail == "" {
		return "the node answered " + strconv.Itoa(e.Code)
	}
	return "the node answered " + strconv.Itoa(e.Code) + ": " + e.Detail
}

// OutboundTestResult is one outbound tested on one node; see cmd/node's
// outboundTestResult, whose wire form this mirrors.
type OutboundTestResult struct {
	Tag           string `json:"tag"`
	DelayMS       int64  `json:"delay_ms"`
	StatusCode    int    `json:"status_code"`
	Error         string `json:"error,omitempty"`
	Skipped       string `json:"skipped,omitempty"`
	Interface     string `json:"interface,omitempty"`
	InterfaceHere bool   `json:"interface_here"`
	Via           string `json:"via,omitempty"`
	Fallback      bool   `json:"fallback"`
}

type OutboundTestResponse struct {
	URL     string               `json:"url"`
	Results []OutboundTestResult `json:"results"`
}

// TestOutbounds asks the node at address:port to test the outbounds tagged
// tags through its running core (POST /outbounds/test). The node bounds each
// test and the whole call itself; ctx should allow it about 20 seconds.
func (c *Client) TestOutbounds(ctx context.Context, address string, port int, tags []string) (OutboundTestResponse, error) {
	var out OutboundTestResponse
	err := c.post(ctx, address, port, "/outbounds/test", map[string]any{"tags": tags}, &out)
	return out, err
}

func (c *Client) post(ctx context.Context, address string, port int, path string, body, into any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	host := net.JoinHostPort(address, strconv.Itoa(port))
	endpoint := url.URL{Scheme: "https", Host: host, Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("cannot reach the node's control port %s: %w", host, err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return fmt.Errorf("read the node's answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Detail string `json:"detail"`
		}
		_ = json.Unmarshal(answer, &e)
		return &StatusError{Code: resp.StatusCode, Detail: e.Detail}
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("the node's answer is not valid: %w", err)
	}
	return nil
}
