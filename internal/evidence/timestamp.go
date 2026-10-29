package evidence

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
)

// An RFC 3161 timestamp from an outside Time Stamping Authority (TSA) proves
// that a pack existed no later than the TSA's time. Without one, a pack's time
// is only what the server that holds the signing key says: whoever has the key
// could sign a new pack about the past. The timestamp covers the signature
// value, which in turn covers every byte of the pack.

// ErrTimestampMismatch means the timestamp does not belong to this pack's
// signature, or its own signature is broken: the pack or the token was changed.
var ErrTimestampMismatch = errors.New("timestamp does not match this evidence pack")

// ErrTimestampUntrusted means the timestamp is intact but its signer does not
// chain to a trusted root as a time-stamping certificate.
var ErrTimestampUntrusted = errors.New("timestamp is not from a trusted time stamping authority")

const maxTSAResponse = 1 << 20

// TimestampInfo describes a verified timestamp.
type TimestampInfo struct {
	Time   time.Time
	TSA    string // signer certificate subject
	Serial string
}

// RequestTimestamp asks the TSA at url to timestamp env's signature and stores
// the token in env.Timestamp. The token's signature and its match with the
// request are checked; whether the TSA is trusted is VerifyTimestamp's job.
func RequestTimestamp(ctx context.Context, client *http.Client, url string, env *Envelope) error {
	if len(env.Signature.Value) == 0 {
		return errors.New("pack is not signed")
	}
	nonce, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return err
	}
	req, err := timestamp.CreateRequest(bytes.NewReader(env.Signature.Value), &timestamp.RequestOptions{
		Hash:         crypto.SHA256,
		Certificates: true, // the token must carry the TSA certificate to be verifiable offline
		Nonce:        nonce,
	})
	if err != nil {
		return fmt.Errorf("build timestamp request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(req))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/timestamp-query")
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("timestamp authority: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTSAResponse))
	if err != nil {
		return fmt.Errorf("timestamp authority: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("timestamp authority answered %s", resp.Status)
	}
	ts, err := timestamp.ParseResponse(body)
	if err != nil {
		return fmt.Errorf("timestamp authority response: %w", err)
	}
	if ts.Nonce == nil || ts.Nonce.Cmp(nonce) != 0 {
		return errors.New("timestamp authority response: nonce does not match the request")
	}
	env.Timestamp, env.TimestampChain = ts.RawToken, nil
	p7, err := checkToken(env)
	if err != nil {
		env.Timestamp = nil
		return err
	}
	env.TimestampChain = fetchIntermediates(ctx, client, p7)
	return nil
}

// fetchIntermediates follows the Authority Information Access URLs from the
// TSA's certificate up to (not including) a self-signed root, for the CA
// certificates the token does not carry. Public TSAs ship only their own
// certificate. It is best effort: what cannot be fetched is left out, and
// VerifyTimestamp then reports the chain as untrusted.
func fetchIntermediates(ctx context.Context, client *http.Client, p7 *pkcs7.PKCS7) [][]byte {
	cur := p7.GetOnlySigner()
	if cur == nil {
		return nil
	}
	var chain [][]byte
	for hop := 0; hop < 4 && !selfSigned(cur); hop++ {
		if issuer := issuerIn(cur, p7.Certificates); issuer != nil {
			cur = issuer
			continue
		}
		if len(cur.IssuingCertificateURL) == 0 {
			return chain
		}
		issuer, err := fetchCert(ctx, client, cur.IssuingCertificateURL[0])
		if err != nil || !bytes.Equal(issuer.RawSubject, cur.RawIssuer) || cur.CheckSignatureFrom(issuer) != nil {
			return chain
		}
		if selfSigned(issuer) {
			return chain // roots come from the verifier's trust store, never from here
		}
		chain = append(chain, issuer.Raw)
		cur = issuer
	}
	return chain
}

func selfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(c) == nil
}

func issuerIn(c *x509.Certificate, certs []*x509.Certificate) *x509.Certificate {
	for _, cand := range certs {
		if cand != c && bytes.Equal(cand.RawSubject, c.RawIssuer) && c.CheckSignatureFrom(cand) == nil {
			return cand
		}
	}
	return nil
}

// fetchCert downloads a CA certificate as DER, PEM or a PKCS#7 bundle.
func fetchCert(ctx context.Context, client *http.Client, url string) (*x509.Certificate, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	if c, err := x509.ParseCertificate(data); err == nil {
		return c, nil
	}
	if block, _ := pem.Decode(data); block != nil {
		return x509.ParseCertificate(block.Bytes)
	}
	if p7, err := pkcs7.Parse(data); err == nil && len(p7.Certificates) > 0 {
		return p7.Certificates[0], nil
	}
	return nil, fmt.Errorf("%s: not a certificate", url)
}

// VerifyTimestamp checks env.Timestamp: that it covers env's signature and is
// intact (else ErrTimestampMismatch), and that its signer chains to roots with
// the time-stamping key usage, valid at the stamped time (else
// ErrTimestampUntrusted, with the info still returned). A nil roots pool means
// the system roots.
func VerifyTimestamp(env *Envelope, roots *x509.CertPool) (*TimestampInfo, error) {
	p7, err := checkToken(env)
	if err != nil {
		return nil, err
	}
	ts, err := timestamp.Parse(env.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestampMismatch, err)
	}
	info := &TimestampInfo{Time: ts.Time.UTC()}
	if ts.SerialNumber != nil {
		info.Serial = ts.SerialNumber.String()
	}
	if signer := p7.GetOnlySigner(); signer != nil {
		info.TSA = signer.Subject.String()
	}
	if roots == nil {
		if roots, err = x509.SystemCertPool(); err != nil {
			return info, fmt.Errorf("%w: no system roots: %v", ErrTimestampUntrusted, err)
		}
	}
	intermediates := x509.NewCertPool()
	for _, c := range p7.Certificates {
		intermediates.AddCert(c)
	}
	for _, der := range env.TimestampChain {
		if c, err := x509.ParseCertificate(der); err == nil {
			intermediates.AddCert(c)
		}
	}
	// The signer is found by issuer and serial in the token, so a trusted
	// certificate merely included next to another signer does not count.
	err = p7.VerifyWithOpts(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   ts.Time,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
	if err != nil {
		return info, fmt.Errorf("%w: %v", ErrTimestampUntrusted, err)
	}
	return info, nil
}

// checkToken verifies that the token is intact (its CMS signature against
// the certificate it names) and that it timestamps env's signature.
func checkToken(env *Envelope) (*pkcs7.PKCS7, error) {
	if len(env.Timestamp) == 0 {
		return nil, errors.New("pack has no timestamp")
	}
	p7, err := pkcs7.Parse(env.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestampMismatch, err)
	}
	if len(p7.Certificates) == 0 {
		return nil, fmt.Errorf("%w: token carries no TSA certificate", ErrTimestampMismatch)
	}
	if err := p7.Verify(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestampMismatch, err)
	}
	ts, err := timestamp.Parse(env.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTimestampMismatch, err)
	}
	if !ts.HashAlgorithm.Available() {
		return nil, fmt.Errorf("%w: unsupported hash %v", ErrTimestampMismatch, ts.HashAlgorithm)
	}
	h := ts.HashAlgorithm.New()
	h.Write(env.Signature.Value)
	if !bytes.Equal(h.Sum(nil), ts.HashedMessage) {
		return nil, fmt.Errorf("%w: it covers another signature", ErrTimestampMismatch)
	}
	return p7, nil
}

// LoadCertPool reads PEM certificates from path; an empty path returns nil,
// which VerifyTimestamp takes as the system roots.
func LoadCertPool(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("%s holds no PEM certificates", path)
	}
	return pool, nil
}
