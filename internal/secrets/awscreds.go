package secrets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// imdsBase is the instance metadata service. Tests point it at httptest.
// It is not an environment variable: a user-controlled URL would be an SSRF switch.
var imdsBase = "http://169.254.169.254"

type awsCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

var (
	imdsOnce   sync.Once
	imdsHTTP   *http.Client
	imdsCredMu sync.Mutex
	imdsCached awsCreds
	imdsUntil  time.Time
)

// imdsClient dials only the link-local metadata address and never follows
// redirects. The secrets broker's other client refuses 169.254.169.254 on
// purpose; this one exists only for the API process on the instance.
func imdsClient() *http.Client {
	imdsOnce.Do(func() {
		dialer := &net.Dialer{Timeout: 2 * time.Second}
		imdsHTTP = &http.Client{
			Timeout: 2 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					host, _, err := net.SplitHostPort(address)
					if err != nil || host != imdsHost() {
						return nil, fmt.Errorf("imds: refusing to dial %s", address)
					}
					return dialer.DialContext(ctx, network, address)
				},
			},
		}
	})
	return imdsHTTP
}

// imdsHost is the only address the metadata client will dial. Production
// keeps the link-local address; tests retarget imdsBase at a local server.
func imdsHost() string {
	u, err := url.Parse(imdsBase)
	if err != nil || u.Hostname() == "" {
		return "169.254.169.254"
	}
	return u.Hostname()
}

func awsCredentials(ctx context.Context) (awsCreds, error) {
	id := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if id != "" || secret != "" {
		if id == "" || secret == "" {
			return awsCreds{}, fmt.Errorf("aws: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must both be set")
		}
		return awsCreds{AccessKeyID: id, SecretAccessKey: secret, SessionToken: os.Getenv("AWS_SESSION_TOKEN")}, nil
	}
	return imdsCredentials(ctx)
}

func imdsCredentials(ctx context.Context) (awsCreds, error) {
	imdsCredMu.Lock()
	defer imdsCredMu.Unlock()
	if imdsCached.AccessKeyID != "" && time.Now().Before(imdsUntil) {
		return imdsCached, nil
	}
	token, err := imdsToken(ctx)
	if err != nil {
		return awsCreds{}, err
	}
	roleRaw, err := imdsGet(ctx, "/latest/meta-data/iam/security-credentials/", token)
	if err != nil {
		return awsCreds{}, err
	}
	role := strings.TrimSpace(string(roleRaw))
	if i := strings.IndexByte(role, '\n'); i >= 0 {
		role = strings.TrimSpace(role[:i])
	}
	if role == "" || strings.Contains(role, "/") || strings.Contains(role, "..") {
		return awsCreds{}, fmt.Errorf("aws: instance metadata returned no role")
	}
	body, err := imdsGet(ctx, "/latest/meta-data/iam/security-credentials/"+role, token)
	if err != nil {
		return awsCreds{}, err
	}
	var doc struct {
		Code            string `json:"Code"`
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		Token           string `json:"Token"`
		Expiration      string `json:"Expiration"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return awsCreds{}, fmt.Errorf("aws: decode instance credentials: %w", err)
	}
	if doc.Code != "Success" || doc.AccessKeyID == "" || doc.SecretAccessKey == "" {
		return awsCreds{}, fmt.Errorf("aws: instance role credentials are not available")
	}
	until := time.Now().Add(5 * time.Minute)
	if exp, err := time.Parse(time.RFC3339, doc.Expiration); err == nil {
		until = exp.Add(-time.Minute)
		if !until.After(time.Now()) {
			until = time.Now().Add(30 * time.Second)
		}
	}
	imdsCached = awsCreds{AccessKeyID: doc.AccessKeyID, SecretAccessKey: doc.SecretAccessKey, SessionToken: doc.Token}
	imdsUntil = until
	return imdsCached, nil
}

func imdsRegion(ctx context.Context) (string, error) {
	token, err := imdsToken(ctx)
	if err != nil {
		return "", err
	}
	body, err := imdsGet(ctx, "/latest/meta-data/placement/region", token)
	if err != nil {
		return "", err
	}
	region := strings.TrimSpace(string(body))
	if region == "" || strings.ContainsAny(region, "/ \n") {
		return "", fmt.Errorf("aws: instance metadata returned no region")
	}
	return region, nil
}

func imdsToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, imdsBase+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "21600")
	resp, err := imdsClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("aws: instance metadata token: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK || len(raw) == 0 {
		return "", fmt.Errorf("aws: instance metadata token failed (status %d)", resp.StatusCode)
	}
	return string(raw), nil
}

func imdsGet(ctx context.Context, path, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imdsBase+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-aws-ec2-metadata-token", token)
	resp, err := imdsClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("aws: instance metadata: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("aws: instance metadata %s failed (status %d)", path, resp.StatusCode)
	}
	return raw, nil
}
