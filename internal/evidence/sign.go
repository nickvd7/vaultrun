package evidence

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrBadSignature means the pack was changed after it was signed, or was
// signed with another key.
var ErrBadSignature = errors.New("evidence signature does not match")

// Signer signs packs with an Ed25519 private key.
type Signer struct {
	priv  ed25519.PrivateKey
	keyID string
}

// ParseSigningKey reads EVIDENCE_SIGNING_KEY: either 64 hex characters (a
// 32-byte Ed25519 seed, as `make up` generates) or the path to a PKCS#8 PEM
// private key file.
func ParseSigningKey(value string) (*Signer, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("empty signing key")
	}
	if seed, err := hex.DecodeString(value); err == nil {
		if len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("hex signing key must be %d bytes (%d hex characters), got %d bytes",
				ed25519.SeedSize, 2*ed25519.SeedSize, len(seed))
		}
		return newSigner(ed25519.NewKeyFromSeed(seed)), nil
	}
	data, err := os.ReadFile(value)
	if err != nil {
		return nil, fmt.Errorf("signing key is neither hex nor a readable file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("signing key file is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse signing key: %w", err)
	}
	priv, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("signing key is %T, want an Ed25519 key", key)
	}
	return newSigner(priv), nil
}

func newSigner(priv ed25519.PrivateKey) *Signer {
	return &Signer{priv: priv, keyID: KeyID(priv.Public().(ed25519.PublicKey))}
}

// KeyID names the signer's public key.
func (s *Signer) KeyID() string { return s.keyID }

// PublicKey returns the verification key.
func (s *Signer) PublicKey() ed25519.PublicKey { return s.priv.Public().(ed25519.PublicKey) }

// PublicKeyPEM returns the verification key as a PKIX PEM block, the form
// `vaultrun evidence verify --key` reads.
func (s *Signer) PublicKeyPEM() []byte { return PublicKeyPEM(s.PublicKey()) }

// Sign encodes p and signs the encoded bytes.
func (s *Signer) Sign(p *Pack) (*Envelope, error) {
	if p.Schema != PackSchema {
		return nil, fmt.Errorf("unsupported pack schema %q", p.Schema)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("encode pack: %w", err)
	}
	return &Envelope{
		Pack: raw,
		Signature: Signature{
			Alg:   SignatureAlg,
			KeyID: s.keyID,
			Value: ed25519.Sign(s.priv, raw),
		},
	}, nil
}

// Verify checks an exported envelope against pub and returns the pack. It
// fails with ErrBadSignature when a single byte of the pack differs from what
// was signed.
func Verify(envelopeJSON []byte, pub ed25519.PublicKey) (*Pack, error) {
	var env Envelope
	if err := json.Unmarshal(envelopeJSON, &env); err != nil {
		return nil, fmt.Errorf("not an evidence pack: %w", err)
	}
	if env.Signature.Alg != SignatureAlg {
		return nil, fmt.Errorf("unsupported signature algorithm %q", env.Signature.Alg)
	}
	if len(env.Pack) == 0 {
		return nil, errors.New("evidence pack is empty")
	}
	if want := KeyID(pub); env.Signature.KeyID != want {
		return nil, fmt.Errorf("%w: pack was signed with key %s, this public key is %s",
			ErrBadSignature, env.Signature.KeyID, want)
	}
	if !ed25519.Verify(pub, env.Pack, env.Signature.Value) {
		return nil, ErrBadSignature
	}
	var p Pack
	if err := json.Unmarshal(env.Pack, &p); err != nil {
		return nil, fmt.Errorf("decode signed pack: %w", err)
	}
	if p.Schema != PackSchema {
		return nil, fmt.Errorf("unsupported pack schema %q", p.Schema)
	}
	return &p, nil
}

// GenerateKey returns a new signing key as a hex seed (the
// EVIDENCE_SIGNING_KEY form) and its public key as PEM.
func GenerateKey() (seedHex string, pubPEM []byte, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(priv.Seed()), PublicKeyPEM(pub), nil
}

// PublicKeyPEM encodes pub as a PKIX "PUBLIC KEY" PEM block.
func PublicKeyPEM(pub ed25519.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		// Only fails for key types x509 does not know; Ed25519 is known.
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// ParsePublicKeyPEM reads a PKIX PEM public key and requires it to be Ed25519.
func ParsePublicKeyPEM(data []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("public key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	pub, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is %T, want an Ed25519 key", key)
	}
	return pub, nil
}
