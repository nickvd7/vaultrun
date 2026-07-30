package browser

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Manager provides browser automation capabilities for VaultRun sessions
type Manager interface {
	// Navigate to a URL
	Navigate(ctx context.Context, sessionID uuid.UUID, url string, opts NavigateOpts) error

	// Take a screenshot
	Screenshot(ctx context.Context, sessionID uuid.UUID, opts ScreenshotOpts) (*ScreenshotResult, error)

	// Click an element
	Click(ctx context.Context, sessionID uuid.UUID, selector string) error

	// Fill an input field
	Fill(ctx context.Context, sessionID uuid.UUID, selector, value string) error

	// Extract content from the page
	Extract(ctx context.Context, sessionID uuid.UUID, opts ExtractOpts) (string, error)

	// Evaluate JavaScript
	Evaluate(ctx context.Context, sessionID uuid.UUID, script string) (interface{}, error)

	// Wait for an element or condition
	Wait(ctx context.Context, sessionID uuid.UUID, opts WaitOpts) error

	// Generate a PDF
	PDF(ctx context.Context, sessionID uuid.UUID, opts PDFOpts) (*PDFResult, error)

	// Close browser resources for a session
	Close(ctx context.Context, sessionID uuid.UUID) error
}

// NavigateOpts configures navigation behavior
type NavigateOpts struct {
	WaitUntil string // "load", "domcontentloaded", "networkidle"
	Timeout   int    // milliseconds
}

// ScreenshotOpts configures screenshot capture
type ScreenshotOpts struct {
	FullPage bool   // Capture entire page (not just viewport)
	Selector string // Screenshot specific element only
	Format   string // "png" or "jpeg"
}

// ScreenshotResult contains screenshot artifact info
type ScreenshotResult struct {
	ArtifactID uuid.UUID
	Path       string
	SizeBytes  int64
	Format     string
}

// ExtractOpts configures content extraction
type ExtractOpts struct {
	Selector string // CSS selector (empty = entire page)
	Extract  string // "text", "html", "attributes"
}

// WaitOpts configures wait conditions
type WaitOpts struct {
	Selector string // Wait for element to appear
	Timeout  int    // milliseconds
}

// PDFOpts configures PDF generation
type PDFOpts struct {
	Format string // "A4", "Letter"
	Path   string // Output path (optional)
}

// PDFResult contains PDF artifact info
type PDFResult struct {
	ArtifactID uuid.UUID
	Path       string
	SizeBytes  int64
}

// BrowserSession tracks browser state for a session
type BrowserSession struct {
	SessionID uuid.UUID
	CreatedAt time.Time
	LastUsed  time.Time
	PageCount int
}

// NetworkPolicy defines allowed/blocked URLs for browser navigation
type NetworkPolicy struct {
	AllowedHosts    []string
	BlockPrivateIPs bool
	BlockLocalhost  bool
	BlockMetadata   bool // Block cloud metadata endpoints (169.254.169.254, etc.)
}

// DefaultNetworkPolicy returns a secure default policy
func DefaultNetworkPolicy() *NetworkPolicy {
	return &NetworkPolicy{
		AllowedHosts:    []string{}, // Empty = allow all
		BlockPrivateIPs: true,
		BlockLocalhost:  true,
		BlockMetadata:   true,
	}
}
