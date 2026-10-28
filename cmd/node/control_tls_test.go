package main

import (
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/certs"
)

func TestControlTLSAcceptsPanelAndRejectsLegacyNodeClient(t *testing.T) {
	ca, key, err := certs.GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	panel, err := tls.X509KeyPair([]byte(ca.CertPEM), []byte(ca.KeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	rootBlock, _ := pem.Decode([]byte(ca.CertPEM))
	root, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	// Older releases issued nodes with both client and server usages.
	leaf := &x509.Certificate{SerialNumber: big.NewInt(77), Subject: pkix.Name{CommonName: "legacy-node"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	legacy := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	server.TLS = controlTLSConfig(panel, pool)
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name     string
		cert     tls.Certificate
		accepted bool
	}{{"panel", panel, true}, {"legacy node", legacy, false}, {"anonymous", tls.Certificate{}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{tc.cert}}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
			resp, err := client.Get(server.URL)
			if resp != nil {
				resp.Body.Close()
			}
			if (err == nil) != tc.accepted {
				t.Fatalf("accepted=%v, want %v", err == nil, tc.accepted)
			}
		})
	}
}
