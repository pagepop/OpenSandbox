package internal_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixed time isolates distrust from expiry, host clocks and live DNS/TLS.
var emSignVerificationTime = time.Date(2026, time.October, 28, 0, 0, 0, 0, time.UTC)

func TestEmSignCompromisedChainsAreRejected(t *testing.T) {
	rootPEM, err := os.ReadFile("testdata/emsign-ca/roots.pem")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatal("incident root fixtures could not be parsed")
	}
	files, err := filepath.Glob("testdata/emsign-ca/*we-have-a-problem.com.pem")
	if err != nil || len(files) != 14 {
		t.Fatalf("expected 12 incident chains and two no-CT variants, got %d: %v", len(files), err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var chain []*x509.Certificate
			for len(data) > 0 {
				block, rest := pem.Decode(data)
				if block == nil {
					if strings.TrimSpace(string(data)) != "" {
						t.Fatal("invalid certificate fixture")
					}
					break
				}
				if block.Type != "CERTIFICATE" {
					t.Fatalf("unexpected PEM type: %s", block.Type)
				}
				cert, err := x509.ParseCertificate(block.Bytes)
				if err != nil {
					t.Fatal(err)
				}
				chain = append(chain, cert)
				data = rest
			}
			if len(chain) < 2 {
				t.Fatal("fixture must contain a leaf and its compromised intermediate")
			}
			intermediates := x509.NewCertPool()
			for _, cert := range chain[1:] {
				intermediates.AddCert(cert)
			}
			// Deliberately trust the historical roots: a repaired Go verifier must
			// reject the compromised intermediate even with a stale/custom bundle.
			_, err = chain[0].Verify(x509.VerifyOptions{
				DNSName:       strings.TrimSuffix(filepath.Base(file), ".pem"),
				Roots:         roots,
				Intermediates: intermediates,
				CurrentTime:   emSignVerificationTime,
			})
			if err == nil {
				t.Fatal("compromised CA chain was accepted; rebuild with a repaired Go toolchain")
			}
		})
	}
}

func TestEmSignDistrustPreservesOrdinaryTLSChain(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ordinary test root"},
		NotBefore: emSignVerificationTime.Add(-time.Hour), NotAfter: emSignVerificationTime.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	root, err = x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"ordinary.test"},
		NotBefore: root.NotBefore, NotAfter: root.NotAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err = x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: "ordinary.test", CurrentTime: emSignVerificationTime}); err != nil {
		t.Fatalf("ordinary TLS certificate was rejected: %v", err)
	}
}
