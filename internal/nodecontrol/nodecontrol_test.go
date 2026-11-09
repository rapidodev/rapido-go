package nodecontrol

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/certs"
)

type fleet struct {
	ca       *certs.PEMPair
	nodeCert tls.Certificate
	pool     *x509.CertPool
}

var (
	fleetOnce sync.Once
	shared    fleet
	fleetErr  error
)

// testFleet is one CA and one node certificate issued by it, the way the panel
// makes them; RSA-4096 is slow, so every test shares them.
func testFleet(t *testing.T) fleet {
	t.Helper()
	fleetOnce.Do(func() {
		ca, key, err := certs.GenerateCA()
		if err != nil {
			fleetErr = err
			return
		}
		leaf, err := certs.SignNodeCert("node-1", ca.CertPEM, key)
		if err != nil {
			fleetErr = err
			return
		}
		nodeCert, err := tls.X509KeyPair([]byte(leaf.CertPEM), []byte(leaf.KeyPEM))
		if err != nil {
			fleetErr = err
			return
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(ca.CertPEM))
		shared = fleet{ca: ca, nodeCert: nodeCert, pool: pool}
	})
	if fleetErr != nil {
		t.Fatal(fleetErr)
	}
	return shared
}

// startNode serves handler the way a node agent does: its own certificate,
// and only the panel's CA identity accepted as a client.
func startNode(t *testing.T, cert tls.Certificate, clientCAs *x509.CertPool, handler http.Handler) (string, int) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 || !cs.PeerCertificates[0].IsCA {
				return errors.New("control access requires the panel identity")
			}
			return nil
		},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	host, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p
}

func newClient(t *testing.T, f fleet) *Client {
	t.Helper()
	c, err := New(f.ca.CertPEM, f.ca.KeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func TestTestOutboundsTalksToANodeOfThisPanel(t *testing.T) {
	f := testFleet(t)
	var got struct {
		Tags      []string          `json:"tags"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	host, port := startNode(t, f.nodeCert, f.pool, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/outbounds/test" {
			http.NotFound(w, r)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"url":"https://www.gstatic.com/generate_204","results":[{"tag":"exit","delay_ms":74,"status_code":204,"interface_here":false,"fallback":false},{"tag":"new","delay_ms":120,"status_code":204,"interface_here":false,"fallback":false,"draft":true}]}`))
	}))

	draft := json.RawMessage(`{"tag":"new","type":"vless","server":"203.0.113.5","server_port":443}`)
	resp, err := newClient(t, f).TestOutbounds(context.Background(), host, port, OutboundTestRequest{Tags: []string{"exit"}, Outbounds: []json.RawMessage{draft}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "exit" || len(got.Outbounds) != 1 || string(got.Outbounds[0]) != string(draft) {
		t.Errorf("node was asked for %v and %s", got.Tags, got.Outbounds)
	}
	if len(resp.Results) != 2 || resp.Results[0].DelayMS != 74 || resp.Results[0].StatusCode != 204 || resp.Results[0].Draft {
		t.Errorf("resp = %+v", resp)
	}
	if !resp.Results[1].Draft || resp.Results[1].DelayMS != 120 {
		t.Errorf("draft result = %+v", resp.Results[1])
	}
}

func TestTestOutboundsRefusesANodeCertificateFromAnotherCA(t *testing.T) {
	f := testFleet(t)
	host, port := startNode(t, foreignCert(t), f.pool, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request reached a node the panel must not trust")
	}))
	_, err := newClient(t, f).TestOutbounds(context.Background(), host, port, OutboundTestRequest{Tags: []string{"exit"}})
	if err == nil || !strings.Contains(err.Error(), "not issued by this panel") {
		t.Errorf("err = %v, want the certificate refused", err)
	}
}

func TestTestOutboundsExplainsAnAnswerOtherThan200(t *testing.T) {
	f := testFleet(t)
	host, port := startNode(t, f.nodeCert, f.pool, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/outbounds/test" {
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"detail":"core is not started"}`))
			return
		}
		http.NotFound(w, r)
	}))
	c := newClient(t, f)
	_, err := c.TestOutbounds(context.Background(), host, port, OutboundTestRequest{Tags: []string{"exit"}})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusConflict || !strings.Contains(err.Error(), "core is not started") {
		t.Errorf("err = %v, want the node's 409 and its detail", err)
	}

	// An agent from before outbound tests answers 404.
	err = c.post(context.Background(), host, port, "/elsewhere", map[string]any{}, &struct{}{})
	if err == nil || !strings.Contains(err.Error(), "too old") {
		t.Errorf("err = %v, want the agent called too old", err)
	}
}

func TestTestOutboundsSaysWhenTheNodeCannotBeReached(t *testing.T) {
	f := testFleet(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err = newClient(t, f).TestOutbounds(context.Background(), "127.0.0.1", port, OutboundTestRequest{Tags: []string{"exit"}})
	if err == nil || !strings.Contains(err.Error(), "cannot reach the node's control port") {
		t.Errorf("err = %v", err)
	}
}

// foreignCert is a server certificate from a CA this panel never saw.
func foreignCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(9), Subject: pkix.Name{CommonName: "node-1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
