package templates

import (
	"testing"

	"github.com/google/uuid"
)

func TestBuiltInTemplates(t *testing.T) {
	if len(BuiltInTemplates) != 4 {
		t.Fatalf("expected 4 built-in templates, got %d", len(BuiltInTemplates))
	}

	// Verify all templates have required fields
	for _, tmpl := range BuiltInTemplates {
		if tmpl.ID == uuid.Nil {
			t.Error("template ID is nil")
		}
		if tmpl.Slug == "" {
			t.Error("template slug is empty")
		}
		if tmpl.Name == "" {
			t.Error("template name is empty")
		}
		if tmpl.Description == "" {
			t.Error("template description is empty")
		}
		if tmpl.Category == "" {
			t.Error("template category is empty")
		}
		if tmpl.Image == "" {
			t.Error("template image is empty")
		}
		if tmpl.Resources.CPULimit <= 0 {
			t.Error("template CPU limit must be > 0")
		}
		if tmpl.Resources.MemoryLimitMB <= 0 {
			t.Error("template memory limit must be > 0")
		}
		if tmpl.Resources.TimeoutSeconds <= 0 {
			t.Error("template timeout must be > 0")
		}
	}
}

func TestTemplateStructure(t *testing.T) {
	// Test ResourceConfig
	rc := ResourceConfig{
		CPULimit:       2.0,
		MemoryLimitMB:  4096,
		TimeoutSeconds: 3600,
	}
	if rc.CPULimit != 2.0 {
		t.Errorf("unexpected CPU limit: %f", rc.CPULimit)
	}

	// Test NetworkConfig
	nc := NetworkConfig{
		Enabled:      true,
		AllowedHosts: []string{"github.com", "pypi.org"},
	}
	if !nc.Enabled {
		t.Error("network should be enabled")
	}
	if len(nc.AllowedHosts) != 2 {
		t.Errorf("expected 2 allowed hosts, got %d", len(nc.AllowedHosts))
	}
}

func TestTemplateFilter(t *testing.T) {
	filter := TemplateFilter{
		Category:  "data-science",
		Tags:      []string{"python", "jupyter"},
		Search:    "machine learning",
		Featured:  true,
		Published: true,
		Limit:     20,
		Offset:    0,
	}

	if filter.Category != "data-science" {
		t.Errorf("unexpected category: %s", filter.Category)
	}
	if len(filter.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(filter.Tags))
	}
}

func TestCreateTemplateRequest(t *testing.T) {
	req := CreateTemplateRequest{
		Slug:        "test-template",
		Name:        "Test Template",
		Description: "A test template",
		Category:    "testing",
		Tags:        []string{"test"},
		Image:       "python:3.12-slim",
		Version:     "1.0.0",
		Resources: ResourceConfig{
			CPULimit:       1.0,
			MemoryLimitMB:  512,
			TimeoutSeconds: 600,
		},
		Network: NetworkConfig{
			Enabled:      false,
			AllowedHosts: []string{},
		},
		Environment: map[string]string{
			"TEST_ENV": "value",
		},
	}

	if req.Slug != "test-template" {
		t.Errorf("unexpected slug: %s", req.Slug)
	}
	if req.Resources.CPULimit != 1.0 {
		t.Errorf("unexpected CPU limit: %f", req.Resources.CPULimit)
	}
	if req.Environment["TEST_ENV"] != "value" {
		t.Error("environment variable not set correctly")
	}
}

// Integration tests would require a database connection
// They are commented out to avoid CI failures

/*
func TestManagerCreate(t *testing.T) {
	// This would require a test database
	db := setupTestDB(t)
	defer db.Close()
	
	m := New(db)
	ctx := context.Background()
	
	req := CreateTemplateRequest{
		Slug:        "test-template",
		Name:        "Test Template",
		Description: "A test template",
		Category:    "testing",
		Image:       "python:3.12-slim",
		Resources: ResourceConfig{
			CPULimit:       1.0,
			MemoryLimitMB:  512,
			TimeoutSeconds: 600,
		},
		Network: NetworkConfig{
			Enabled:      false,
			AllowedHosts: []string{},
		},
	}
	
	orgID := uuid.New()
	tmpl, err := m.Create(ctx, orgID, req)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	
	if tmpl.Slug != req.Slug {
		t.Errorf("expected slug %s, got %s", req.Slug, tmpl.Slug)
	}
}

func TestManagerGet(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	
	m := New(db)
	ctx := context.Background()
	
	// Bootstrap built-in templates
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	
	// Get first built-in template
	tmpl, err := m.Get(ctx, BuiltInTemplates[0].ID)
	if err != nil {
		t.Fatalf("get template: %v", err)
	}
	
	if tmpl.Slug != BuiltInTemplates[0].Slug {
		t.Errorf("expected slug %s, got %s", BuiltInTemplates[0].Slug, tmpl.Slug)
	}
}

func TestManagerList(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	
	m := New(db)
	ctx := context.Background()
	
	if err := m.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	
	filter := TemplateFilter{
		Published: true,
		Limit:     10,
		Offset:    0,
	}
	
	templates, err := m.List(ctx, filter)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	
	if len(templates) != 4 {
		t.Errorf("expected 4 templates, got %d", len(templates))
	}
}
*/
