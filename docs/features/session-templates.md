# Session Templates Marketplace

## Executive Summary

Pre-configured session templates met pre-installed tools, dependencies, en best-practice configurations. Marketplace voor community-contributed templates. One-click setup voor common use cases zoals "Python Data Science", "Node.js API Development", "Rust Development".

**Status:** 💡 Prioriteit 3  
**Effort:** Medium (2-3 weken)  
**Dependencies:** Docker image registry, template validation system

---

## Problem Statement

### Current Onboarding Experience

Creating a new session requires:

1. **Choosing base image** — `python:3.12-slim` vs `python:3.12` vs custom?
2. **Installing dependencies** — `pip install pandas numpy matplotlib scipy ...`
3. **Configuring environment** — env vars, configs, ssh keys
4. **Setting resource limits** — How much CPU/memory do I need?
5. **Applying policies** — What's a reasonable security policy?

**Time to productive sandbox:** 10-30 minutes ⏰

**For common use cases (Python ML, Node.js API, etc.):**
- Everyone installs same packages
- Everyone makes same configuration mistakes
- Everyone wastes time reinventing the wheel

---

## Solution Overview

### Templates Marketplace

```
┌──────────────────────────────────────────────────────┐
│ Create New Session                                   │
├──────────────────────────────────────────────────────┤
│                                                      │
│  Start from template:                                │
│                                                      │
│  📊 Python Data Science          ⭐️ 342 uses       │
│     Python 3.12 + Jupyter + pandas + numpy          │
│     [Use Template]                                   │
│                                                      │
│  🚀 Node.js API Development      ⭐️ 256 uses       │
│     Node 20 + Express + Postgres client             │
│     [Use Template]                                   │
│                                                      │
│  🦀 Rust Development              ⭐️ 89 uses        │
│     Latest Rust + common crates                     │
│     [Use Template]                                   │
│                                                      │
│  🌐 Web Scraping                  ⭐️ 147 uses       │
│     Python + Playwright + Chrome                    │
│     [Use Template]                                   │
│                                                      │
│  [Browse All Templates] [Create Custom]             │
└──────────────────────────────────────────────────────┘
```

**Benefits:**
- ⚡️ **Fast setup** — 0 minutes instead of 10-30
- ✅ **Best practices** — Curated configs and tools
- 🔒 **Security** — Pre-configured policies
- 🤝 **Community** — Share and discover templates

---

## Architecture

### Template Structure

```yaml
# template.yaml
name: "Python Data Science"
slug: "python-data-science"
description: "Python 3.12 with Jupyter, pandas, numpy, matplotlib, and scikit-learn"
author: "VaultRun Team"
version: "1.2.0"
category: "data-science"
tags:
  - python
  - jupyter
  - machine-learning
  - data-analysis

# Docker image
image: "vaultrun/python-data-science:latest"

# Pre-installed packages (for documentation)
packages:
  python:
    - jupyter==1.0.0
    - pandas==2.1.0
    - numpy==1.25.0
    - matplotlib==3.8.0
    - scikit-learn==1.3.0
    - seaborn==0.12.0

# Default resource limits
resources:
  cpu_limit: 2.0
  memory_limit_mb: 4096
  timeout_seconds: 7200  # 2 hours

# Network policy
network:
  enabled: true
  allowed_hosts:
    - github.com
    - pypi.org
    - kaggle.com

# Security policy (natural language)
policy: |
  Python data analysis sandbox.
  Allow network to github.com, pypi.org, and kaggle.com.
  Max 2 CPU, 4GB memory, 2 hour timeout.
  Allow python, pip, jupyter, git.
  Block curl, wget, ssh.

# Environment variables
env:
  JUPYTER_ENABLE_LAB: "yes"
  MPLBACKEND: "Agg"  # Matplotlib non-interactive backend

# Startup script (runs on session creation)
startup_script: |
  #!/bin/bash
  # Start Jupyter Lab in background
  jupyter lab --ip=0.0.0.0 --port=8888 --no-browser --allow-root &
  
  # Download sample dataset
  wget -q https://example.com/sample-data.csv -O /workspace/sample-data.csv
  
  echo "Jupyter Lab started on port 8888"
  echo "Sample dataset available at /workspace/sample-data.csv"

# README (shown in template details)
readme: |
  # Python Data Science Template
  
  Pre-configured environment for data analysis and machine learning.
  
  ## Included Tools
  - Python 3.12
  - Jupyter Lab (port 8888)
  - pandas, numpy, matplotlib, scikit-learn, seaborn
  
  ## Quick Start
  1. Create session from this template
  2. Access Jupyter Lab at http://localhost:8888
  3. Start analyzing data!
  
  ## Example Notebook
  A sample notebook is available in `/workspace/example.ipynb`.
```

### Template Registry

**Storage:**
- Templates stored in database (`templates` table)
- Docker images stored in Docker Hub or private registry
- Template files (YAML) versioned in Git repo

**Discovery:**
- Browse templates by category (data-science, web-dev, devops, etc.)
- Search by tags, language, tools
- Sort by popularity (usage count), rating, recency

---

## Data Model

### Database Schema

```sql
CREATE TABLE templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(100) UNIQUE NOT NULL,  -- e.g. 'python-data-science'
    name VARCHAR(255) NOT NULL,
    description TEXT,
    readme TEXT,
    
    -- Authorship
    author_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    author_name VARCHAR(255),  -- For VaultRun Team or external authors
    
    -- Docker image
    image VARCHAR(500) NOT NULL,
    
    -- Template config (YAML as JSONB)
    config JSONB NOT NULL,
    
    -- Metadata
    category VARCHAR(100),  -- data-science, web-dev, mobile, devops, etc.
    tags TEXT[],
    version VARCHAR(20),
    
    -- Stats
    use_count INT DEFAULT 0,
    rating_avg DECIMAL(3, 2),  -- 0.00 - 5.00
    rating_count INT DEFAULT 0,
    
    -- Visibility
    visibility VARCHAR(20) DEFAULT 'public',  -- public, private, org
    org_id UUID REFERENCES orgs(id) ON DELETE CASCADE,  -- For org-private templates
    
    -- Status
    published BOOLEAN DEFAULT false,
    verified BOOLEAN DEFAULT false,  -- Verified by VaultRun team
    
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX idx_templates_category ON templates(category);
CREATE INDEX idx_templates_published ON templates(published, use_count DESC);
CREATE INDEX idx_templates_org ON templates(org_id) WHERE org_id IS NOT NULL;

CREATE TABLE template_ratings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES templates(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rating INT NOT NULL CHECK (rating >= 1 AND rating <= 5),
    review TEXT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    
    UNIQUE(template_id, user_id)
);

CREATE INDEX idx_template_ratings_template ON template_ratings(template_id);
```

---

## Implementation Plan

### Phase 1: Template System (Week 1)

- [ ] Create `templates` table schema
- [ ] Implement `internal/templates/` package
- [ ] CRUD operations for templates
- [ ] Template validation (YAML schema)
- [ ] Session creation from template

### Phase 2: Docker Images (Week 1-2)

- [ ] Build official templates:
  - Python Data Science
  - Node.js API Development
  - Rust Development
  - Web Scraping
  - Go Development
  - Java Spring Boot
- [ ] Publish images to Docker Hub (`vaultrun/*`)
- [ ] Create template YAMLs for each image
- [ ] Seed database with official templates

### Phase 3: API & MCP (Week 2)

- [ ] API endpoints:
  - `GET /api/v1/templates` — List templates
  - `GET /api/v1/templates/:slug` — Get template details
  - `POST /api/v1/templates` — Create custom template
  - `POST /api/v1/sessions/from-template/:slug` — Create session from template
- [ ] MCP tools:
  - `template_list`
  - `template_get`
  - `session_create_from_template`

### Phase 4: Community Features (Week 2-3)

- [ ] Template ratings and reviews
- [ ] Template usage tracking
- [ ] User-contributed templates (submit for review)
- [ ] Template verification process

### Phase 5: Dashboard UI (Week 3)

- [ ] Template marketplace page
- [ ] Template detail page
- [ ] "Create from Template" flow
- [ ] My Templates page (user-created templates)
- [ ] Template builder UI

---

## API Examples

### List Templates

```bash
GET /api/v1/templates?category=data-science&limit=10

Response:
{
  "templates": [
    {
      "id": "tmpl_001",
      "slug": "python-data-science",
      "name": "Python Data Science",
      "description": "Python 3.12 with Jupyter, pandas, numpy, matplotlib",
      "author": "VaultRun Team",
      "image": "vaultrun/python-data-science:latest",
      "category": "data-science",
      "tags": ["python", "jupyter", "machine-learning"],
      "use_count": 342,
      "rating_avg": 4.7,
      "verified": true
    },
    {
      "id": "tmpl_002",
      "slug": "r-statistical-analysis",
      "name": "R Statistical Analysis",
      "description": "R 4.3 with RStudio, tidyverse, ggplot2",
      "author": "Community",
      "image": "vaultrun/r-stats:latest",
      "category": "data-science",
      "tags": ["r", "statistics"],
      "use_count": 87,
      "rating_avg": 4.5,
      "verified": false
    }
  ]
}
```

### Get Template Details

```bash
GET /api/v1/templates/python-data-science

Response:
{
  "id": "tmpl_001",
  "slug": "python-data-science",
  "name": "Python Data Science",
  "description": "Python 3.12 with Jupyter, pandas, numpy, matplotlib, and scikit-learn",
  "author": "VaultRun Team",
  "version": "1.2.0",
  
  "image": "vaultrun/python-data-science:latest",
  
  "config": {
    "resources": {
      "cpu_limit": 2.0,
      "memory_limit_mb": 4096,
      "timeout_seconds": 7200
    },
    "network": {
      "enabled": true,
      "allowed_hosts": ["github.com", "pypi.org", "kaggle.com"]
    },
    "env": {
      "JUPYTER_ENABLE_LAB": "yes",
      "MPLBACKEND": "Agg"
    }
  },
  
  "packages": {
    "python": ["jupyter==1.0.0", "pandas==2.1.0", "numpy==1.25.0", ...]
  },
  
  "readme": "# Python Data Science Template\n\nPre-configured environment...",
  
  "use_count": 342,
  "rating_avg": 4.7,
  "rating_count": 89,
  "verified": true,
  
  "created_at": "2026-01-15T10:00:00Z",
  "updated_at": "2026-07-01T14:30:00Z"
}
```

### Create Session from Template

```bash
POST /api/v1/sessions/from-template/python-data-science
{
  "name": "my-analysis-project",
  "overrides": {
    "resources": {
      "memory_limit_mb": 8192  # Override template default
    }
  }
}

Response:
{
  "id": "sess_abc123",
  "name": "my-analysis-project",
  "status": "running",
  "image": "vaultrun/python-data-science:latest",
  "template_id": "tmpl_001",
  "template_slug": "python-data-science",
  "cpu_limit": 2.0,
  "memory_limit_mb": 8192,  # Overridden
  ...
}
```

---

## MCP Tools

### New Tools

```go
{
    Name:        "template_list",
    Description: "List available session templates",
    InputSchema: {
        "category": "string (optional)",
        "tags": "array (optional)",
        "limit": "number (optional)",
    },
}

{
    Name:        "template_get",
    Description: "Get details of a specific template",
    InputSchema: {
        "slug": "string (required)",
    },
}

{
    Name:        "session_create_from_template",
    Description: "Create a new session from a template",
    InputSchema: {
        "template_slug": "string (required)",
        "name": "string (optional)",
        "overrides": "object (optional) - override template defaults",
    },
}

{
    Name:        "template_create",
    Description: "Create a custom template",
    InputSchema: {
        "name": "string (required)",
        "image": "string (required)",
        "config": "object (required)",
        "visibility": "string (optional) - public|private|org",
    },
}
```

---

## Official Templates

### 1. Python Data Science

```yaml
name: "Python Data Science"
slug: "python-data-science"
image: "vaultrun/python-data-science:latest"
category: "data-science"
packages:
  - jupyter, pandas, numpy, matplotlib, scikit-learn, seaborn
resources:
  cpu: 2.0, memory: 4096MB
```

### 2. Node.js API Development

```yaml
name: "Node.js API Development"
slug: "nodejs-api"
image: "vaultrun/nodejs-api:latest"
category: "web-dev"
packages:
  - express, pg, redis, jest, eslint
resources:
  cpu: 2.0, memory: 2048MB
network:
  allowed: github.com, npmjs.org
```

### 3. Rust Development

```yaml
name: "Rust Development"
slug: "rust-dev"
image: "vaultrun/rust-dev:latest"
category: "systems"
packages:
  - cargo, rustfmt, clippy, common crates
resources:
  cpu: 4.0, memory: 4096MB
```

### 4. Web Scraping

```yaml
name: "Web Scraping"
slug: "web-scraping"
image: "vaultrun/browser:playwright-python"
category: "automation"
packages:
  - playwright, beautifulsoup4, requests, selenium
resources:
  cpu: 2.0, memory: 2048MB
network:
  enabled: true  # All hosts (scraping)
```

### 5. Go Development

```yaml
name: "Go Development"
slug: "go-dev"
image: "vaultrun/go-dev:latest"
category: "backend"
packages:
  - go 1.25, common modules
resources:
  cpu: 2.0, memory: 2048MB
```

### 6. Java Spring Boot

```yaml
name: "Java Spring Boot"
slug: "java-spring-boot"
image: "vaultrun/java-spring:latest"
category: "backend"
packages:
  - JDK 21, Maven, Spring Boot 3.2
resources:
  cpu: 2.0, memory: 4096MB
```

---

## Community Contributions

### Submission Process

1. **Create template** — User builds Docker image + YAML config
2. **Submit for review** — `POST /api/v1/templates/submit`
3. **VaultRun review** — Security scan, policy check, testing
4. **Approval** — Template published to marketplace with "Community" badge
5. **Verification** — Popular templates get verified badge after manual review

### Template Validation

```go
type TemplateValidator struct {
    scanner       ImageScanner  // Trivy/Snyk/Aqua
    docker        *docker.Client
    db            *sql.DB
}

func (v *TemplateValidator) Validate(template *Template) error {
    // 1. Image security scanning
    if err := v.validateImageSecurity(template.Image); err != nil {
        return fmt.Errorf("image security validation failed: %w", err)
    }
    
    // 2. Startup script security
    if err := v.validateStartupScript(template.Config.StartupScript); err != nil {
        return fmt.Errorf("startup script validation failed: %w", err)
    }
    
    // 3. Policy check
    if template.Config.Network.Enabled && len(template.Config.Network.AllowedHosts) == 0 {
        return errors.New("network enabled without allowed_hosts — security risk")
    }
    
    // 4. Resource limits
    if template.Config.Resources.MemoryLimitMB > 16384 {
        return errors.New("memory limit too high (max 16GB for community templates)")
    }
    
    // 5. Image size
    imageSize := v.getImageSize(template.Image)
    if imageSize > 5*1024*1024*1024 {  // 5GB
        return errors.New("image too large (max 5GB)")
    }
    
    // 6. Name squatting check
    if err := v.checkNameSquatting(template); err != nil {
        return err
    }
    
    return nil
}

// Comprehensive image security scan
func (v *TemplateValidator) validateImageSecurity(image string) error {
    // Pull image
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
    defer cancel()
    
    if err := v.docker.PullImage(ctx, image); err != nil {
        return fmt.Errorf("failed to pull image: %w", err)
    }
    
    // Scan for vulnerabilities
    scanResults, err := v.scanner.Scan(image)
    if err != nil {
        return fmt.Errorf("scan failed: %w", err)
    }
    
    // Reject critical vulnerabilities
    if scanResults.CriticalCount > 0 {
        return fmt.Errorf("%d critical vulnerabilities found", scanResults.CriticalCount)
    }
    
    // Warn on high vulnerabilities
    if scanResults.HighCount > 10 {
        return fmt.Errorf("too many high severity vulnerabilities: %d", scanResults.HighCount)
    }
    
    // Check for suspicious files
    suspiciousPatterns := []string{
        "**/cryptominer*", "**/backdoor*", "**/*.onion",
        "**/reverse_shell*", "**/keylogger*",
    }
    
    for _, pattern := range suspiciousPatterns {
        matches := v.findInImage(image, pattern)
        if len(matches) > 0 {
            return fmt.Errorf("suspicious files found: %v", matches)
        }
    }
    
    // Check for cryptocurrency miners (common in backdoored images)
    minerProcesses := []string{"xmrig", "ethminer", "cpuminer", "minerd"}
    for _, proc := range minerProcesses {
        if v.imageContainsFile(image, proc) {
            return fmt.Errorf("cryptocurrency miner detected: %s", proc)
        }
    }
    
    // Verify image provenance (if available)
    if !v.isOfficialImage(image) && !v.isVerifiedPublisher(image) {
        log.Warn("Image from unverified publisher",
            "image", image,
            "template", template.Name,
        )
    }
    
    return nil
}

// Validate startup script for dangerous commands
func (v *TemplateValidator) validateStartupScript(script string) error {
    if script == "" {
        return nil
    }
    
    // Check length
    if len(script) > 10000 {
        return errors.New("startup script too long (max 10KB)")
    }
    
    // Dangerous commands that should never be in startup scripts
    dangerousCommands := []string{
        "curl http://", "curl https://",  // External downloads
        "wget http://", "wget https://",
        "nc ", "netcat ",                  // Network tools
        "eval ", "exec ",                  // Code execution
        "/dev/tcp/", "/dev/udp/",         // Network redirects
        "rm -rf /", "mkfs",                // Destructive operations
        "iptables", "ip route",            // Network manipulation
        "docker", "kubectl",               // Container escape
        "sudo su", "sudo bash",            // Privilege escalation
    }
    
    scriptLower := strings.ToLower(script)
    for _, cmd := range dangerousCommands {
        if strings.Contains(scriptLower, cmd) {
            return fmt.Errorf("startup script contains dangerous command: %s", cmd)
        }
    }
    
    // Check for obfuscation attempts
    if strings.Count(script, "base64") > 2 {
        return errors.New("suspicious base64 usage in startup script")
    }
    
    if strings.Count(script, "\\x") > 10 {
        return errors.New("suspicious hex encoding in startup script")
    }
    
    // Parse bash and check for redirections to untrusted sources
    // (This would need a proper bash parser, simplified here)
    
    return nil
}

// Check for template name squatting
func (v *TemplateValidator) checkNameSquatting(template *Template) error {
    // Reserved names for official templates
    reservedNames := []string{
        "python-data-science", "nodejs-api", "rust-dev",
        "go-dev", "java-spring", "web-scraping",
    }
    
    if contains(reservedNames, template.Slug) && template.AuthorID != OfficialAuthorID {
        return errors.New("template name reserved for official use")
    }
    
    // Check similarity to existing templates
    existing := v.db.GetPublishedTemplates()
    for _, e := range existing {
        if e.ID == template.ID {
            continue  // Skip self
        }
        
        similarity := levenshteinDistance(template.Name, e.Name)
        if similarity < 3 {
            return fmt.Errorf("template name too similar to existing '%s'", e.Name)
        }
        
        // Check slug similarity
        if levenshteinDistance(template.Slug, e.Slug) < 2 {
            return fmt.Errorf("template slug too similar to existing '%s'", e.Slug)
        }
    }
    
    return nil
}

// Levenshtein distance for string similarity
func levenshteinDistance(s1, s2 string) int {
    // Implementation omitted for brevity
    // Returns edit distance between two strings
}
```

---

## Dashboard UI

### Template Marketplace

```
┌────────────────────────────────────────────────────────┐
│ Template Marketplace                                   │
├────────────────────────────────────────────────────────┤
│                                                        │
│  [Search templates...]              [🔍]               │
│                                                        │
│  Categories: [All] [Data Science] [Web Dev] [Mobile]  │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │ 📊 Python Data Science          ⭐️ 4.7 (89)     │ │
│  │ Python 3.12 + Jupyter + pandas + numpy          │ │
│  │ By VaultRun Team • 342 uses • ✓ Verified       │ │
│  │ [Use Template] [View Details]                   │ │
│  └──────────────────────────────────────────────────┘ │
│                                                        │
│  ┌──────────────────────────────────────────────────┐ │
│  │ 🚀 Node.js API Development      ⭐️ 4.6 (67)     │ │
│  │ Node 20 + Express + Postgres client             │ │
│  │ By VaultRun Team • 256 uses • ✓ Verified       │ │
│  │ [Use Template] [View Details]                   │ │
│  └──────────────────────────────────────────────────┘ │
│                                                        │
│  [Load More]                                           │
└────────────────────────────────────────────────────────┘
```

### Template Detail Page

```
┌────────────────────────────────────────────────────────┐
│ Python Data Science                      ⭐️ 4.7 (89)  │
├────────────────────────────────────────────────────────┤
│                                                        │
│  By VaultRun Team • Version 1.2.0 • ✓ Verified        │
│  342 uses • Last updated July 1, 2026                  │
│                                                        │
│  [🚀 Use Template] [⭐️ Rate] [📤 Share]                │
│                                                        │
│  ──────────────────────────────────────────────────── │
│                                                        │
│  ## Description                                        │
│  Pre-configured environment for data analysis and      │
│  machine learning with Python 3.12, Jupyter Lab, and   │
│  popular data science libraries.                       │
│                                                        │
│  ## Included Tools                                     │
│  • Python 3.12                                         │
│  • Jupyter Lab (port 8888)                            │
│  • pandas, numpy, matplotlib, scikit-learn            │
│                                                        │
│  ## Configuration                                      │
│  • CPU: 2.0 cores                                     │
│  • Memory: 4096 MB                                    │
│  • Network: github.com, pypi.org, kaggle.com         │
│                                                        │
│  ## Reviews (89)                                       │
│  ⭐️⭐️⭐️⭐️⭐️ "Perfect for ML projects!" - Alice (Jul 28) │
│  ⭐️⭐️⭐️⭐️☆ "Great, but needs TensorFlow" - Bob (Jul 25) │
│                                                        │
└────────────────────────────────────────────────────────┘
```

---

## Configuration

```bash
# Templates feature
TEMPLATES_ENABLED=true

# Docker registry for template images
TEMPLATE_REGISTRY=docker.io  # or custom registry
TEMPLATE_ORG=vaultrun         # Docker Hub org

# Community submissions
TEMPLATE_SUBMISSION_ENABLED=true
TEMPLATE_AUTO_PUBLISH=false   # Require manual approval

# Image validation
TEMPLATE_MAX_IMAGE_SIZE_GB=5
TEMPLATE_SCAN_ENABLED=true
TEMPLATE_SCAN_PROVIDER=trivy  # or snyk, aqua

# Marketplace
TEMPLATE_FEATURED_COUNT=6     # Featured templates on homepage
```

---

## Testing Strategy

### Unit Tests

- [ ] Template YAML parsing and validation
- [ ] Session creation from template
- [ ] Template override logic
- [ ] Rating aggregation

### Integration Tests

- [ ] Create session from each official template
- [ ] Verify packages are installed
- [ ] Verify startup script executes
- [ ] Verify network policy is applied

### E2E Tests

- [ ] Full marketplace flow (browse → view → create)
- [ ] Community template submission
- [ ] Rating and review submission

---

## Success Metrics

- % of sessions created from templates (target: 50%)
- Most popular templates (usage counts)
- User ratings (target avg: 4.5+)
- Community template submissions per month

---

## Future Enhancements

### Phase 2 Features

1. **Template inheritance** — Extend existing templates
   ```yaml
   extends: "python-data-science"
   additional_packages:
     - tensorflow
     - keras
   ```

2. **Template generator** — AI-powered template creation
   - "Create a template for React Native development"
   - Auto-generates Dockerfile + YAML

3. **Private marketplace** — Org-specific templates
   - Company-specific tooling
   - Internal Docker registries

4. **Template collections** — Bundles of related templates
   - "Full-Stack Development" (frontend + backend + db)

5. **Template forking** — Clone and customize existing templates

6. **Template CI/CD** — Auto-build images on git push
   - Template repo → GitHub Actions → Docker Hub → VaultRun

7. **Template metrics** — Track package usage, success rates
   - "95% of sessions from this template complete successfully"

---

## References

- Docker Hub: https://hub.docker.com/u/vaultrun
- Template validation: `internal/templates/validator.go`
- Session creation: `internal/runner/runner.go`
- Existing images: `deployments/docker/`
