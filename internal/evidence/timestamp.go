package evidence

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
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
	env.Timestamp = ts.RawToken
	if _, err := checkToken(env); err != nil {
		env.Timestamp = nil
		return err
	}
	return nil
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
	// The signer is found by issuer and serial in the token, so a trusted
	// certificate merely included next to another signer does not count.
	err = p7.VerifyWithOpts(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: ts.Time,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
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
