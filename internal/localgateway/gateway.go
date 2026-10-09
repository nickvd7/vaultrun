package localgateway

import (
	"context"
	"io"

	vaultrun "github.com/nickvd7/vaultrun/sdk/go"
)

// VaultRunClient is the subset of the Go SDK used by the gateway.
type VaultRunClient interface {
	CreateSession(ctx context.Context, opts vaultrun.CreateSessionOptions) (*vaultrun.Session, error)
	GetSession(ctx context.Context, sessionID string) (*vaultrun.Session, error)
	DeleteSession(ctx context.Context, sessionID string) error
	Run(ctx context.Context, sessionID string, opts vaultrun.RunOptions) (*vaultrun.Run, error)
	UploadFile(ctx context.Context, sessionID, remotePath string, content io.Reader) (*vaultrun.File, error)
	DownloadFile(ctx context.Context, sessionID, remotePath string) (io.ReadCloser, error)
	ListFiles(ctx context.Context, sessionID string) ([]*vaultrun.File, error)
}

// Gateway is the local inference action plane.
type Gateway struct {
	cfg      Config
	vr       VaultRunClient
	upstream ChatUpstream
	missions MissionAPI
	sessions *SessionStore
	limiter  *ipRateLimiter
}

// New constructs a Gateway.
func New(cfg Config, vr VaultRunClient, upstream ChatUpstream) *Gateway {
	return &Gateway{
		cfg:      cfg,
		vr:       vr,
		upstream: upstream,
		sessions: NewSessionStore(),
		limiter:  newIPRateLimiter(cfg.RateLimitPerMin),
	}
}

// WithMissions attaches a mission capture backend (optional).
func (g *Gateway) WithMissions(m MissionAPI) *Gateway {
	g.missions = m
	return g
}

// NewFromConfig builds VaultRun + upstream clients from config.
func NewFromConfig(cfg Config) *Gateway {
	vr := vaultrun.New(cfg.VaultRunBaseURL, cfg.VaultRunAPIKey)
	up := NewHTTPUpstream(cfg.UpstreamURL, cfg.UpstreamTimeout)
	g := New(cfg, vr, up)
	if cfg.CaptureMissions {
		g.WithMissions(newHTTPMissionAPI(cfg.VaultRunBaseURL, cfg.VaultRunAPIKey))
	}
	return g
}
