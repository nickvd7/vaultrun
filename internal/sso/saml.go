package sso

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

// SAMLProvider wraps a SAML 2.0 Service Provider.
type SAMLProvider struct {
	sp      saml.ServiceProvider
	rootURL *url.URL
}

// SAMLClaims holds identity information extracted from a SAML assertion.
type SAMLClaims struct {
	NameID string // SAML NameID (typically email or user UUID)
	Email  string // Attribute: mail / email / emailAddress
	Name   string // Attribute: displayName / cn / name
}

// NewSAMLProvider creates a SAML Service Provider.
//
//   - rootURL:       public base URL of this server (e.g. "https://api.example.com")
//   - entityID:      SP entity ID (defaults to rootURL/auth/saml/metadata)
//   - idpMetaURL:    URL of the IdP metadata XML
//   - certFile/key:  PEM cert + key for SP-side signing/encryption
func NewSAMLProvider(ctx context.Context, rootURL, entityID, idpMetaURL, certFile, keyFile string) (*SAMLProvider, error) {
	root, err := url.Parse(rootURL)
	if err != nil {
		return nil, fmt.Errorf("saml: invalid root URL: %w", err)
	}

	keyPair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("saml: load cert/key: %w", err)
	}
	keyPair.Leaf, err = x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("saml: parse leaf cert: %w", err)
	}
	rsaKey, ok := keyPair.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("saml: private key must be RSA")
	}

	meta, err := fetchIDPMetadata(ctx, idpMetaURL)
	if err != nil {
		return nil, fmt.Errorf("saml: fetch IdP metadata: %w", err)
	}

	if entityID == "" {
		entityID = rootURL + "/auth/saml/metadata"
	}
	entityURL, _ := url.Parse(entityID)

	sp := saml.ServiceProvider{
		Key:         rsaKey,
		Certificate: keyPair.Leaf,
		MetadataURL: *entityURL,
		AcsURL:      *root,
		IDPMetadata: meta,
	}
	// Override AcsURL to point to our handler
	acsURL, _ := url.Parse(rootURL + "/auth/saml/acs")
	sp.AcsURL = *acsURL

	return &SAMLProvider{sp: sp, rootURL: root}, nil
}

// MetadataXML returns the SP metadata as XML bytes for IdP configuration.
func (p *SAMLProvider) MetadataXML() ([]byte, error) {
	meta := p.sp.Metadata()
	return xml.MarshalIndent(meta, "", "  ")
}

// LoginURL returns the URL to redirect the browser to for SAML login.
func (p *SAMLProvider) LoginURL() (string, error) {
	authReq, err := p.sp.MakeAuthenticationRequest(
		p.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding),
		saml.HTTPRedirectBinding,
		saml.HTTPPostBinding,
	)
	if err != nil {
		return "", fmt.Errorf("saml: make auth request: %w", err)
	}
	redirect, err := authReq.Redirect("", &p.sp)
	if err != nil {
		return "", fmt.Errorf("saml: build redirect: %w", err)
	}
	return redirect.String(), nil
}

// ParseResponse validates the HTTP-POST binding SAMLResponse and extracts claims.
func (p *SAMLProvider) ParseResponse(r *http.Request) (*SAMLClaims, error) {
	if err := r.ParseForm(); err != nil {
		return nil, fmt.Errorf("saml: parse form: %w", err)
	}

	assertion, err := p.sp.ParseResponse(r, nil)
	if err != nil {
		return nil, fmt.Errorf("saml: parse response: %w", err)
	}

	claims := &SAMLClaims{NameID: assertion.Subject.NameID.Value}

	for _, stmt := range assertion.AttributeStatements {
		for _, attr := range stmt.Attributes {
			vals := attrValues(attr)
			if len(vals) == 0 {
				continue
			}
			switch attr.Name {
			case "mail", "email", "emailAddress",
				"urn:oid:0.9.2342.19200300.100.1.3",
				"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress":
				claims.Email = vals[0]
			case "displayName", "cn", "name",
				"urn:oid:2.16.840.1.113730.3.1.241",
				"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name":
				claims.Name = vals[0]
			}
		}
	}

	if claims.Email == "" {
		claims.Email = claims.NameID
	}

	return claims, nil
}

func attrValues(attr saml.Attribute) []string {
	out := make([]string, 0, len(attr.Values))
	for _, v := range attr.Values {
		if v.Value != "" {
			out = append(out, v.Value)
		}
	}
	return out
}

func fetchIDPMetadata(ctx context.Context, metaURL string) (*saml.EntityDescriptor, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", metaURL, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata URL returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return samlsp.ParseMetadata(body)
}
