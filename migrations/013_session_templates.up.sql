-- Session Templates Marketplace
CREATE TABLE IF NOT EXISTS session_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT NOT NULL,
    category VARCHAR(50) NOT NULL,
    tags TEXT[] DEFAULT '{}',
    image VARCHAR(500) NOT NULL,
    author VARCHAR(200) NOT NULL,
    author_org UUID REFERENCES organizations(id) ON DELETE SET NULL,
    version VARCHAR(20) NOT NULL DEFAULT '1.0.0',
    published BOOLEAN NOT NULL DEFAULT false,
    featured BOOLEAN NOT NULL DEFAULT false,
    use_count INTEGER NOT NULL DEFAULT 0,
    
    -- Configuration (JSON columns)
    resources JSONB NOT NULL DEFAULT '{"cpu_limit": 1.0, "memory_limit_mb": 512, "timeout_seconds": 3600}',
    network JSONB NOT NULL DEFAULT '{"enabled": false, "allowed_hosts": []}',
    environment JSONB DEFAULT '{}',
    policy TEXT,
    
    -- Metadata
    packages JSONB DEFAULT '{}',
    readme TEXT,
    startup_script TEXT,
    
    created_at TIMESTAMP NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Indexes for common queries
CREATE INDEX idx_templates_slug ON session_templates(slug);
CREATE INDEX idx_templates_category ON session_templates(category);
CREATE INDEX idx_templates_published ON session_templates(published);
CREATE INDEX idx_templates_featured ON session_templates(featured, published);
CREATE INDEX idx_templates_use_count ON session_templates(use_count DESC);
CREATE INDEX idx_templates_tags ON session_templates USING GIN(tags);

-- Full-text search index
CREATE INDEX idx_templates_search ON session_templates USING GIN(
    to_tsvector('english', name || ' ' || description)
);

-- Template usage tracking
CREATE TABLE IF NOT EXISTS template_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id UUID NOT NULL REFERENCES session_templates(id) ON DELETE CASCADE,
    session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_template_usage_template ON template_usage(template_id);
CREATE INDEX idx_template_usage_session ON template_usage(session_id);
CREATE INDEX idx_template_usage_org ON template_usage(org_id);
CREATE INDEX idx_template_usage_created ON template_usage(created_at);

-- Trigger to increment use_count
CREATE OR REPLACE FUNCTION increment_template_use_count()
RETURNS TRIGGER AS $$
BEGIN
    UPDATE session_templates
    SET use_count = use_count + 1
    WHERE id = NEW.template_id;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_increment_template_use_count
AFTER INSERT ON template_usage
FOR EACH ROW
EXECUTE FUNCTION increment_template_use_count();

-- Add template_id to sessions table (optional reference)
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS template_id UUID REFERENCES session_templates(id) ON DELETE SET NULL;
CREATE INDEX idx_sessions_template ON sessions(template_id);
