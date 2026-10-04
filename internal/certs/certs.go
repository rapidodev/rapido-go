// Package certs generates the Rapido-branded certificate material used for
// panel<->node mTLS: one self-signed Rapido CA issues the panel's own
// identity and signs every node's leaf certificate, so every certificate in
// the fleet carries a consistent, Rapido-branded CN.
package certs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"
)

const (
	// CommonName is used for the panel's own certificate and as the CA's
	// subject. Every node leaf certificate is issued with its own node name
	// as CN, signed by this CA.
	CommonName = "Rapido"
	validFor   = 100 * 365 * 24 * time.Hour
	keyBits    = 4096
)

type PEMPair struct {
	CertPEM string
	KeyPEM  string
}

// ParseRSAPrivateKeyPEM reads back a private key produced by GenerateCA (or
// SignNodeCert) - needed wherever the CA's key must be loaded from storage
// (the `tls` table) to sign another certificate later, rather than kept
// only in the memory of the process that generated it.
func ParseRSAPrivateKeyPEM(keyPEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return nil, fmt.Errorf("certs: invalid private key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parse private key: %w", err)
	}
	return key, nil
}

// GenerateCA creates a new self-signed Rapido CA/panel identity, matching
// the current generate_certificate() contract (RSA-4096, self-signed,
// CN="Rapido", 100-year validity) but usable as a CA to sign node leaf
// certificates too (CA:true, KeyUsage includes cert signing).
func GenerateCA() (*PEMPair, *rsa.PrivateKey, error) {
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("certs: generate serial: %w", err)
	}

	notBefore := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: CommonName},
		NotBefore:             notBefore,
		NotAfter:              notBefore.Add(validFor),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		BasicConstraintsValid: true,
		IsCA:                  true,
		SignatureAlgorithm:    x509.SHA512WithRSA,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("certs: create certificate: %w", err)
	}

	return &PEMPair{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	}, key, nil
}

// GenerateSelfSignedLeaf creates a standalone self-signed server
// certificate (not a CA, not chained to the Rapido CA above at all) for an
// inbound's own TLS - hysteria2/tuic/anytls all need one, and real VPN
// clients for these protocols validate it by pinned fingerprint or not at
// all (the "allow_insecure" convention this whole proxy use case already
// relies on), never through a trusted root, so self-signed is the correct
// default here, not a shortcut. Used to auto-provision an admin-created
// inbound that didn't bring its own certificate (see POST /api/inbounds),
// so "+ add this protocol" produces something that actually starts instead
// of sitting inert until someone pastes in a certificate by hand.
func GenerateSelfSignedLeaf(commonName string) (*PEMPair, error) {
	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, fmt.Errorf("certs: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("certs: generate serial: %w", err)
	}

	notBefore := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: commonName},
		DNSNames:           []string{commonName},
		NotBefore:          notBefore,
		NotAfter:           notBefore.Add(validFor),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: x509.SHA512WithRSA,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("certs: create certificate: %w", err)
	}

	return &PEMPair{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	}, nil
}

// SignNodeCert issues a leaf certificate for a node, signed by the Rapido
// CA produced by GenerateCA. Used in the node-agent phase to give every
// node a certificate the panel's CA (and thus the panel itself) trusts.
func SignNodeCert(nodeName string, caCertPEM string, caKey *rsa.PrivateKey) (*PEMPair, error) {
	block, _ := pem.Decode([]byte(caCertPEM))
	if block == nil {
		return nil, fmt.Errorf("certs: invalid CA certificate PEM")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: parse CA certificate: %w", err)
	}

	key, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, fmt.Errorf("certs: generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("certs: generate serial: %w", err)
	}

	notBefore := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: nodeName},
		NotBefore:          notBefore,
		NotAfter:           notBefore.Add(validFor),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		SignatureAlgorithm: x509.SHA512WithRSA,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("certs: create certificate: %w", err)
	}

	return &PEMPair{
		CertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		KeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	}, nil
}
