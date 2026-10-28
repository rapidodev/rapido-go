package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

func controlTLSConfig(cert tls.Certificate, authorities *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert}, ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs: authorities, MinVersion: tls.VersionTLS12,
		VerifyConnection: func(cs tls.ConnectionState) error {
			// Normal chain validation runs first. A legacy node leaf could
			// include ClientAuth; only the panel's trusted CA identity controls nodes.
			if len(cs.PeerCertificates) == 0 || !cs.PeerCertificates[0].IsCA {
				return fmt.Errorf("control access requires the panel identity")
			}
			return nil
		},
	}
}
