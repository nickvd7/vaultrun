// Package evidencetest provides an in-process RFC 3161 Time Stamping
// Authority for tests, with its own root CA.
package evidencetest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/digitorus/timestamp"
)

// TSA is a test time stamping authority.
type TSA struct {
	URL      string
	Roots    *x509.CertPool // trusts this TSA's root
	RootPEM  []byte
	RootFile string // RootPEM written to a temp file, for --tsa-ca style flags

	// Behaviour switches for negative tests.
	IgnoreNonce bool // answer without the request's nonce
	Fail        bool // answer 503

	AIAFetches int // how often the intermediate was downloaded
}

// Options shape the TSA certificate.
type Options struct {
	NoTimeStampingEKU bool // issue a certificate without the time-stamping usage
	// Intermediate issues the TSA certificate from an intermediate CA that
	// the token does not carry; it is only available from the URL in the TSA
	// certificate's Authority Information Access, like public TSAs do.
	Intermediate bool
}

// NewTSA starts a TSA; it stops when the test ends.
func NewTSA(t testing.TB, opts Options) *TSA {
	t.Helper()
	rootKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "VaultRun Test TSA Root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, rootKey.Public(), rootKey)
	if err != nil {
		t.Fatal(err)
	}
	root, _ := x509.ParseCertificate(rootDER)

	// The server is created first so certificates can point at its URL.
	tsa := &TSA{Roots: x509.NewCertPool()}
	var handler http.HandlerFunc
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler(w, r) }))
	t.Cleanup(srv.Close)
	tsa.URL = srv.URL

	issuer, issuerKey := root, rootKey
	var intermediateDER []byte
	if opts.Intermediate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber:          big.NewInt(3),
			Subject:               pkix.Name{CommonName: "VaultRun Test TSA Intermediate"},
			NotBefore:             time.Now().Add(-time.Hour),
			NotAfter:              time.Now().Add(24 * time.Hour),
			IsCA:                  true,
			BasicConstraintsValid: true,
			KeyUsage:              x509.KeyUsageCertSign,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, root, key.Public(), rootKey)
		if err != nil {
			t.Fatal(err)
		}
		intermediateDER = der
		issuer, _ = x509.ParseCertificate(der)
		issuerKey = key
	}

	tsaKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tsaTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "VaultRun Test TSA", Organization: []string{"VaultRun Test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}
	if opts.NoTimeStampingEKU {
		tsaTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	}
	if opts.Intermediate {
		tsaTmpl.IssuingCertificateURL = []string{srv.URL + "/intermediate.crt"}
	}
	tsaDER, err := x509.CreateCertificate(rand.Reader, tsaTmpl, issuer, tsaKey.Public(), issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	tsaCert, _ := x509.ParseCertificate(tsaDER)

	tsa.Roots.AddCert(root)
	tsa.RootPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER})
	tsa.RootFile = filepath.Join(t.TempDir(), "tsa-root.pem")
	if err := os.WriteFile(tsa.RootFile, tsa.RootPEM, 0o644); err != nil {
		t.Fatal(err)
	}

	serial := int64(100)
	handler = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/intermediate.crt" && intermediateDER != nil {
			tsa.AIAFetches++
			w.Header().Set("Content-Type", "application/pkix-cert")
			_, _ = w.Write(intermediateDER)
			return
		}
		if tsa.Fail {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		body, _ := io.ReadAll(r.Body)
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		serial++
		ts := timestamp.Timestamp{
			HashAlgorithm:     req.HashAlgorithm,
			HashedMessage:     req.HashedMessage,
			Time:              time.Now().UTC().Truncate(time.Second),
			Nonce:             req.Nonce,
			Policy:            []int{1, 3, 6, 1, 4, 1, 99999, 1},
			SerialNumber:      big.NewInt(serial),
			AddTSACertificate: req.Certificates,
		}
		if tsa.IgnoreNonce {
			ts.Nonce = nil
		}
		resp, err := ts.CreateResponseWithOpts(tsaCert, tsaKey, crypto.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	}
	return tsa
}
