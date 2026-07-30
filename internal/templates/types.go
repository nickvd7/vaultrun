package templates

import (
	"time"

	"github.com/google/uuid"
)

// Template represents a pre-configured session template
type Template struct {
	ID          uuid.UUID         `db:"id" json:"id"`
	Slug        string            `db:"slug" json:"slug"`                 // URL-friendly identifier
	Name        string            `db:"name" json:"name"`                 // Display name
	Description string            `db:"description" json:"description"`   // Short description
	Category    string            `db:"category" json:"category"`         // Category (data-science, web-dev, etc.)
	Tags        []string          `db:"tags" json:"tags"`                 // Tags for search
	Image       string            `db:"image" json:"image"`               // Docker image
	Author      string            `db:"author" json:"author"`             // Author name
	AuthorOrg   *uuid.UUID        `db:"author_org" json:"author_org"`     // Author organization (nullable)
	Version     string            `db:"version" json:"version"`           // Template version
	Published   bool              `db:"published" json:"published"`       // Whether publicly visible
	Featured    bool              `db:"featured" json:"featured"`         // Featured on homepage
	UseCount    int               `db:"use_count" json:"use_count"`       // Times used
	
	// Configuration
	Resources   ResourceConfig    `db:"resources" json:"resources"`       // Default resource limits
	Network     NetworkConfig     `db:"network" json:"network"`           // Network configuration
	Environment map[string]string `db:"environment" json:"environment"`   // Environment variables
	Policy      string            `db:"policy" json:"policy"`             // Natural language policy
	
	// Metadata
	Packages    map[string][]string `db:"packages" json:"packages"`       // Pre-installed packages
	Readme      string              `db:"readme" json:"readme"`           // Full README (Markdown)
	StartupScript string            `db:"startup_script" json:"startup_script"` // Optional startup script
	
	CreatedAt   time.Time         `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time         `db:"updated_at" json:"updated_at"`
}

// ResourceConfig defines default resource limits
type ResourceConfig struct {
	CPULimit       float64 `json:"cpu_limit"`
	MemoryLimitMB  int     `json:"memory_limit_mb"`
	TimeoutSeconds int     `json:"timeout_seconds"`
}

// NetworkConfig defines network access
type NetworkConfig struct {
	Enabled      bool     `json:"enabled"`
	AllowedHosts []string `json:"allowed_hosts"`
}

// CreateTemplateRequest for API
type CreateTemplateRequest struct {
	Slug          string              `json:"slug" binding:"required"`
	Name          string              `json:"name" binding:"required"`
	Description   string              `json:"description" binding:"required"`
	Category      string              `json:"category" binding:"required"`
	Tags          []string            `json:"tags"`
	Image         string              `json:"image" binding:"required"`
	Version       string              `json:"version"`
	Resources     ResourceConfig      `json:"resources"`
	Network       NetworkConfig       `json:"network"`
	Environment   map[string]string   `json:"environment"`
	Policy        string              `json:"policy"`
	Packages      map[string][]string `json:"packages"`
	Readme        string              `json:"readme"`
	StartupScript string              `json:"startup_script"`
}

// UpdateTemplateRequest for API
type UpdateTemplateRequest struct {
	Name          *string              `json:"name"`
	Description   *string              `json:"description"`
	Category      *string              `json:"category"`
	Tags          []string             `json:"tags"`
	Image         *string              `json:"image"`
	Version       *string              `json:"version"`
	Published     *bool                `json:"published"`
	Featured      *bool                `json:"featured"`
	Resources     *ResourceConfig      `json:"resources"`
	Network       *NetworkConfig       `json:"network"`
	Environment   map[string]string    `json:"environment"`
	Policy        *string              `json:"policy"`
	Packages      map[string][]string  `json:"packages"`
	Readme        *string              `json:"readme"`
	StartupScript *string              `json:"startup_script"`
}

// TemplateFilter for listing/searching
type TemplateFilter struct {
	Category  string   `form:"category"`
	Tags      []string `form:"tags"`
	Search    string   `form:"search"`    // Search in name/description
	Featured  bool     `form:"featured"`
	Published bool     `form:"published"`
	Limit     int      `form:"limit"`
	Offset    int      `form:"offset"`
}

// BuiltInTemplates are shipped with VaultRun
var BuiltInTemplates = []Template{
	{
		ID:          uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		Slug:        "python-data-science",
		Name:        "Python Data Science",
		Description: "Python 3.12 with Jupyter, pandas, numpy, matplotlib, and scikit-learn",
		Category:    "data-science",
		Tags:        []string{"python", "jupyter", "machine-learning", "data-analysis"},
		Image:       "python:3.12-slim",
		Author:      "VaultRun Team",
		Version:     "1.0.0",
		Published:   true,
		Featured:    true,
		Resources: ResourceConfig{
			CPULimit:       2.0,
			MemoryLimitMB:  4096,
			TimeoutSeconds: 7200,
		},
		Network: NetworkConfig{
			Enabled:      true,
			AllowedHosts: []string{"github.com", "pypi.org"},
		},
		Policy: "Python data science environment. Allow github.com and pypi.org. Max 2 CPU, 4GB RAM, 2 hour timeout.",
		Packages: map[string][]string{
			"python": {"jupyter", "pandas", "numpy", "matplotlib", "scikit-learn", "seaborn"},
		},
		Readme: "# Python Data Science\n\nPre-configured for data analysis and machine learning.\n\n## Included\n- Python 3.12\n- Jupyter Lab\n- pandas, numpy, matplotlib\n- scikit-learn, seaborn",
	},
	{
		ID:          uuid.MustParse("00000000-0000-0000-0000-000000000002"),
		Slug:        "nodejs-api",
		Name:        "Node.js API Development",
		Description: "Node.js 20 with Express, TypeScript, and common API tools",
		Category:    "web-dev",
		Tags:        []string{"nodejs", "typescript", "api", "express"},
		Image:       "node:20-slim",
		Author:      "VaultRun Team",
		Version:     "1.0.0",
		Published:   true,
		Featured:    true,
		Resources: ResourceConfig{
			CPULimit:       2.0,
			MemoryLimitMB:  2048,
			TimeoutSeconds: 3600,
		},
		Network: NetworkConfig{
			Enabled:      true,
			AllowedHosts: []string{"github.com", "registry.npmjs.org"},
		},
		Policy: "Node.js API development. Allow github.com and npm registry. Max 2 CPU, 2GB RAM, 1 hour timeout.",
		Packages: map[string][]string{
			"npm": {"express", "typescript", "ts-node", "@types/node", "@types/express"},
		},
		Readme: "# Node.js API Development\n\nPre-configured for REST API development.\n\n## Included\n- Node.js 20\n- TypeScript\n- Express\n- Common type definitions",
	},
	{
		ID:          uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		Slug:        "web-scraping",
		Name:        "Web Scraping",
		Description: "Python with Playwright and headless Chrome for web scraping",
		Category:    "automation",
		Tags:        []string{"python", "playwright", "scraping", "browser"},
		Image:       "vaultrun/browser:playwright-python",
		Author:      "VaultRun Team",
		Version:     "1.0.0",
		Published:   true,
		Featured:    true,
		Resources: ResourceConfig{
			CPULimit:       2.0,
			MemoryLimitMB:  4096,
			TimeoutSeconds: 1800,
		},
		Network: NetworkConfig{
			Enabled:      true,
			AllowedHosts: []string{}, // Allow all for scraping
		},
		Policy: "Web scraping environment. Network enabled. Max 2 CPU, 4GB RAM, 30 minute timeout.",
		Packages: map[string][]string{
			"python": {"playwright", "beautifulsoup4", "requests", "lxml"},
		},
		Readme: "# Web Scraping\n\nHeadless Chrome with Playwright for web scraping.\n\n## Included\n- Python 3.12\n- Playwright + Chromium\n- BeautifulSoup4\n- Requests",
	},
	{
		ID:          uuid.MustParse("00000000-0000-0000-0000-000000000004"),
		Slug:        "rust-dev",
		Name:        "Rust Development",
		Description: "Latest Rust with cargo and common crates",
		Category:    "development",
		Tags:        []string{"rust", "cargo", "systems"},
		Image:       "rust:latest",
		Author:      "VaultRun Team",
		Version:     "1.0.0",
		Published:   true,
		Featured:    false,
		Resources: ResourceConfig{
			CPULimit:       4.0,
			MemoryLimitMB:  8192,
			TimeoutSeconds: 3600,
		},
		Network: NetworkConfig{
			Enabled:      true,
			AllowedHosts: []string{"github.com", "crates.io"},
		},
		Policy: "Rust development. Allow github.com and crates.io. Max 4 CPU, 8GB RAM for compilation.",
		Packages: map[string][]string{
			"cargo": {"serde", "tokio", "reqwest"},
		},
		Readme: "# Rust Development\n\nLatest Rust with cargo.\n\n## Included\n- Latest Rust stable\n- cargo\n- Common crates pre-cached",
	},
}
