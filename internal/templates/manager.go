package templates

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

var (
	ErrTemplateNotFound    = errors.New("template not found")
	ErrTemplateSlugExists  = errors.New("template with this slug already exists")
	ErrInvalidTemplate     = errors.New("invalid template configuration")
)

// Manager handles template operations
type Manager struct {
	db *sqlx.DB
}

// New creates a new template manager
func New(db *sqlx.DB) *Manager {
	return &Manager{db: db}
}

// Create creates a new template
func (m *Manager) Create(ctx context.Context, orgID uuid.UUID, req CreateTemplateRequest) (*Template, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	// Validate slug uniqueness
	var exists bool
	err := m.db.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM session_templates WHERE slug = $1)", req.Slug)
	if err != nil {
		return nil, fmt.Errorf("check slug uniqueness: %w", err)
	}
	if exists {
		return nil, ErrTemplateSlugExists
	}

	// Marshal JSON fields
	resourcesJSON, err := json.Marshal(req.Resources)
	if err != nil {
		return nil, fmt.Errorf("marshal resources: %w", err)
	}

	networkJSON, err := json.Marshal(req.Network)
	if err != nil {
		return nil, fmt.Errorf("marshal network: %w", err)
	}

	envJSON, err := json.Marshal(req.Environment)
	if err != nil {
		return nil, fmt.Errorf("marshal environment: %w", err)
	}

	packagesJSON, err := json.Marshal(req.Packages)
	if err != nil {
		return nil, fmt.Errorf("marshal packages: %w", err)
	}

	version := req.Version
	if version == "" {
		version = "1.0.0"
	}

	tmpl := &Template{
		ID:          uuid.New(),
		Slug:        req.Slug,
		Name:        req.Name,
		Description: req.Description,
		Category:    req.Category,
		Tags:        req.Tags,
		Image:       req.Image,
		Author:      "Custom",
		AuthorOrg:   &orgID,
		Version:     version,
		Published:   false,
		Featured:    false,
		UseCount:    0,
		Resources:   req.Resources,
		Network:     req.Network,
		Environment: req.Environment,
		Policy:      req.Policy,
		Packages:    req.Packages,
		Readme:      req.Readme,
		StartupScript: req.StartupScript,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	query := `
		INSERT INTO session_templates (
			id, slug, name, description, category, tags, image, author, author_org, version,
			published, featured, use_count, resources, network, environment, policy,
			packages, readme, startup_script, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17,
			$18, $19, $20, $21, $22
		)`

	_, err = m.db.ExecContext(ctx, query,
		tmpl.ID, tmpl.Slug, tmpl.Name, tmpl.Description, tmpl.Category, pq.Array(tmpl.Tags),
		tmpl.Image, tmpl.Author, tmpl.AuthorOrg, tmpl.Version,
		tmpl.Published, tmpl.Featured, tmpl.UseCount, resourcesJSON, networkJSON, envJSON, tmpl.Policy,
		packagesJSON, tmpl.Readme, tmpl.StartupScript, tmpl.CreatedAt, tmpl.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert template: %w", err)
	}

	return tmpl, nil
}

// Get retrieves a template by ID
func (m *Manager) Get(ctx context.Context, id uuid.UUID) (*Template, error) {
	return m.getTemplate(ctx, "id = $1", id)
}

// GetBySlug retrieves a template by slug
func (m *Manager) GetBySlug(ctx context.Context, slug string) (*Template, error) {
	return m.getTemplate(ctx, "slug = $1", slug)
}

func (m *Manager) getTemplate(ctx context.Context, condition string, arg interface{}) (*Template, error) {
	var tmpl struct {
		ID            uuid.UUID      `db:"id"`
		Slug          string         `db:"slug"`
		Name          string         `db:"name"`
		Description   string         `db:"description"`
		Category      string         `db:"category"`
		Tags          pq.StringArray `db:"tags"`
		Image         string         `db:"image"`
		Author        string         `db:"author"`
		AuthorOrg     *uuid.UUID     `db:"author_org"`
		Version       string         `db:"version"`
		Published     bool           `db:"published"`
		Featured      bool           `db:"featured"`
		UseCount      int            `db:"use_count"`
		ResourcesJSON []byte         `db:"resources"`
		NetworkJSON   []byte         `db:"network"`
		EnvironmentJSON []byte       `db:"environment"`
		Policy        sql.NullString `db:"policy"`
		PackagesJSON  []byte         `db:"packages"`
		Readme        sql.NullString `db:"readme"`
		StartupScript sql.NullString `db:"startup_script"`
		CreatedAt     time.Time      `db:"created_at"`
		UpdatedAt     time.Time      `db:"updated_at"`
	}

	query := fmt.Sprintf(`
		SELECT id, slug, name, description, category, tags, image, author, author_org, version,
		       published, featured, use_count, resources, network, environment, policy,
		       packages, readme, startup_script, created_at, updated_at
		FROM session_templates
		WHERE %s`, condition)

	err := m.db.GetContext(ctx, &tmpl, query, arg)
	if err == sql.ErrNoRows {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}

	result := &Template{
		ID:          tmpl.ID,
		Slug:        tmpl.Slug,
		Name:        tmpl.Name,
		Description: tmpl.Description,
		Category:    tmpl.Category,
		Tags:        []string(tmpl.Tags),
		Image:       tmpl.Image,
		Author:      tmpl.Author,
		AuthorOrg:   tmpl.AuthorOrg,
		Version:     tmpl.Version,
		Published:   tmpl.Published,
		Featured:    tmpl.Featured,
		UseCount:    tmpl.UseCount,
		CreatedAt:   tmpl.CreatedAt,
		UpdatedAt:   tmpl.UpdatedAt,
	}

	if err := json.Unmarshal(tmpl.ResourcesJSON, &result.Resources); err != nil {
		return nil, fmt.Errorf("unmarshal resources: %w", err)
	}
	if err := json.Unmarshal(tmpl.NetworkJSON, &result.Network); err != nil {
		return nil, fmt.Errorf("unmarshal network: %w", err)
	}
	if err := json.Unmarshal(tmpl.EnvironmentJSON, &result.Environment); err != nil {
		return nil, fmt.Errorf("unmarshal environment: %w", err)
	}
	if err := json.Unmarshal(tmpl.PackagesJSON, &result.Packages); err != nil {
		return nil, fmt.Errorf("unmarshal packages: %w", err)
	}

	if tmpl.Policy.Valid {
		result.Policy = tmpl.Policy.String
	}
	if tmpl.Readme.Valid {
		result.Readme = tmpl.Readme.String
	}
	if tmpl.StartupScript.Valid {
		result.StartupScript = tmpl.StartupScript.String
	}

	return result, nil
}

// List returns templates with optional filtering
func (m *Manager) List(ctx context.Context, filter TemplateFilter) ([]Template, error) {
	conditions := []string{}
	args := []interface{}{}
	argCount := 1

	if filter.Category != "" {
		conditions = append(conditions, fmt.Sprintf("category = $%d", argCount))
		args = append(args, filter.Category)
		argCount++
	}

	if len(filter.Tags) > 0 {
		conditions = append(conditions, fmt.Sprintf("tags && $%d", argCount))
		args = append(args, pq.Array(filter.Tags))
		argCount++
	}

	if filter.Search != "" {
		conditions = append(conditions, fmt.Sprintf("(name ILIKE $%d OR description ILIKE $%d)", argCount, argCount))
		args = append(args, "%"+filter.Search+"%")
		argCount++
	}

	if filter.Featured {
		conditions = append(conditions, "featured = true")
	}

	if filter.Published {
		conditions = append(conditions, "published = true")
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := fmt.Sprintf(`
		SELECT id, slug, name, description, category, tags, image, author, author_org, version,
		       published, featured, use_count, resources, network, environment, policy,
		       packages, readme, startup_script, created_at, updated_at
		FROM session_templates
		%s
		ORDER BY featured DESC, use_count DESC, created_at DESC
		LIMIT $%d OFFSET $%d
	`, whereClause, argCount, argCount+1)

	args = append(args, limit, offset)

	var rows []struct {
		ID            uuid.UUID      `db:"id"`
		Slug          string         `db:"slug"`
		Name          string         `db:"name"`
		Description   string         `db:"description"`
		Category      string         `db:"category"`
		Tags          pq.StringArray `db:"tags"`
		Image         string         `db:"image"`
		Author        string         `db:"author"`
		AuthorOrg     *uuid.UUID     `db:"author_org"`
		Version       string         `db:"version"`
		Published     bool           `db:"published"`
		Featured      bool           `db:"featured"`
		UseCount      int            `db:"use_count"`
		ResourcesJSON []byte         `db:"resources"`
		NetworkJSON   []byte         `db:"network"`
		EnvironmentJSON []byte       `db:"environment"`
		Policy        sql.NullString `db:"policy"`
		PackagesJSON  []byte         `db:"packages"`
		Readme        sql.NullString `db:"readme"`
		StartupScript sql.NullString `db:"startup_script"`
		CreatedAt     time.Time      `db:"created_at"`
		UpdatedAt     time.Time      `db:"updated_at"`
	}

	err := m.db.SelectContext(ctx, &rows, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}

	templates := make([]Template, len(rows))
	for i, row := range rows {
		templates[i] = Template{
			ID:          row.ID,
			Slug:        row.Slug,
			Name:        row.Name,
			Description: row.Description,
			Category:    row.Category,
			Tags:        []string(row.Tags),
			Image:       row.Image,
			Author:      row.Author,
			AuthorOrg:   row.AuthorOrg,
			Version:     row.Version,
			Published:   row.Published,
			Featured:    row.Featured,
			UseCount:    row.UseCount,
			CreatedAt:   row.CreatedAt,
			UpdatedAt:   row.UpdatedAt,
		}

		if err := json.Unmarshal(row.ResourcesJSON, &templates[i].Resources); err != nil {
			return nil, fmt.Errorf("unmarshal resources: %w", err)
		}
		if err := json.Unmarshal(row.NetworkJSON, &templates[i].Network); err != nil {
			return nil, fmt.Errorf("unmarshal network: %w", err)
		}
		if err := json.Unmarshal(row.EnvironmentJSON, &templates[i].Environment); err != nil {
			return nil, fmt.Errorf("unmarshal environment: %w", err)
		}
		if err := json.Unmarshal(row.PackagesJSON, &templates[i].Packages); err != nil {
			return nil, fmt.Errorf("unmarshal packages: %w", err)
		}

		if row.Policy.Valid {
			templates[i].Policy = row.Policy.String
		}
		if row.Readme.Valid {
			templates[i].Readme = row.Readme.String
		}
		if row.StartupScript.Valid {
			templates[i].StartupScript = row.StartupScript.String
		}
	}

	return templates, nil
}

// Update updates a template
func (m *Manager) Update(ctx context.Context, id uuid.UUID, req UpdateTemplateRequest) (*Template, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}

	tmpl, err := m.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	updates := []string{}
	args := []interface{}{}
	argCount := 1

	if req.Name != nil {
		updates = append(updates, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *req.Name)
		argCount++
	}

	if req.Description != nil {
		updates = append(updates, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *req.Description)
		argCount++
	}

	if req.Category != nil {
		updates = append(updates, fmt.Sprintf("category = $%d", argCount))
		args = append(args, *req.Category)
		argCount++
	}

	if req.Tags != nil {
		updates = append(updates, fmt.Sprintf("tags = $%d", argCount))
		args = append(args, pq.Array(req.Tags))
		argCount++
	}

	if req.Image != nil {
		updates = append(updates, fmt.Sprintf("image = $%d", argCount))
		args = append(args, *req.Image)
		argCount++
	}

	if req.Version != nil {
		updates = append(updates, fmt.Sprintf("version = $%d", argCount))
		args = append(args, *req.Version)
		argCount++
	}

	if req.Published != nil {
		updates = append(updates, fmt.Sprintf("published = $%d", argCount))
		args = append(args, *req.Published)
		argCount++
	}

	if req.Featured != nil {
		updates = append(updates, fmt.Sprintf("featured = $%d", argCount))
		args = append(args, *req.Featured)
		argCount++
	}

	if req.Resources != nil {
		resourcesJSON, err := json.Marshal(req.Resources)
		if err != nil {
			return nil, fmt.Errorf("marshal resources: %w", err)
		}
		updates = append(updates, fmt.Sprintf("resources = $%d", argCount))
		args = append(args, resourcesJSON)
		argCount++
	}

	if req.Network != nil {
		networkJSON, err := json.Marshal(req.Network)
		if err != nil {
			return nil, fmt.Errorf("marshal network: %w", err)
		}
		updates = append(updates, fmt.Sprintf("network = $%d", argCount))
		args = append(args, networkJSON)
		argCount++
	}

	if req.Environment != nil {
		envJSON, err := json.Marshal(req.Environment)
		if err != nil {
			return nil, fmt.Errorf("marshal environment: %w", err)
		}
		updates = append(updates, fmt.Sprintf("environment = $%d", argCount))
		args = append(args, envJSON)
		argCount++
	}

	if req.Policy != nil {
		updates = append(updates, fmt.Sprintf("policy = $%d", argCount))
		args = append(args, *req.Policy)
		argCount++
	}

	if req.Packages != nil {
		packagesJSON, err := json.Marshal(req.Packages)
		if err != nil {
			return nil, fmt.Errorf("marshal packages: %w", err)
		}
		updates = append(updates, fmt.Sprintf("packages = $%d", argCount))
		args = append(args, packagesJSON)
		argCount++
	}

	if req.Readme != nil {
		updates = append(updates, fmt.Sprintf("readme = $%d", argCount))
		args = append(args, *req.Readme)
		argCount++
	}

	if req.StartupScript != nil {
		updates = append(updates, fmt.Sprintf("startup_script = $%d", argCount))
		args = append(args, *req.StartupScript)
		argCount++
	}

	if len(updates) == 0 {
		return tmpl, nil
	}

	updates = append(updates, fmt.Sprintf("updated_at = $%d", argCount))
	args = append(args, time.Now())
	argCount++

	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE session_templates
		SET %s
		WHERE id = $%d
	`, strings.Join(updates, ", "), argCount)

	_, err = m.db.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("update template: %w", err)
	}

	return m.Get(ctx, id)
}

// Delete deletes a template
func (m *Manager) Delete(ctx context.Context, id uuid.UUID) error {
	result, err := m.db.ExecContext(ctx, "DELETE FROM session_templates WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}

	if rows == 0 {
		return ErrTemplateNotFound
	}

	return nil
}

// RecordUsage records that a template was used for a session
func (m *Manager) RecordUsage(ctx context.Context, templateID, sessionID, orgID uuid.UUID) error {
	_, err := m.db.ExecContext(ctx,
		"INSERT INTO template_usage (template_id, session_id, org_id) VALUES ($1, $2, $3)",
		templateID, sessionID, orgID,
	)
	if err != nil {
		return fmt.Errorf("record template usage: %w", err)
	}

	return nil
}

// Bootstrap inserts built-in templates if they don't exist
func (m *Manager) Bootstrap(ctx context.Context) error {
	for _, tmpl := range BuiltInTemplates {
		var exists bool
		err := m.db.GetContext(ctx, &exists, "SELECT EXISTS(SELECT 1 FROM session_templates WHERE id = $1)", tmpl.ID)
		if err != nil {
			return fmt.Errorf("check template existence: %w", err)
		}
		if exists {
			continue
		}

		resourcesJSON, _ := json.Marshal(tmpl.Resources)
		networkJSON, _ := json.Marshal(tmpl.Network)
		envJSON, _ := json.Marshal(tmpl.Environment)
		packagesJSON, _ := json.Marshal(tmpl.Packages)

		query := `
			INSERT INTO session_templates (
				id, slug, name, description, category, tags, image, author, author_org, version,
				published, featured, use_count, resources, network, environment, policy,
				packages, readme, startup_script, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16, $17,
				$18, $19, $20, NOW(), NOW()
			)`

		_, err = m.db.ExecContext(ctx, query,
			tmpl.ID, tmpl.Slug, tmpl.Name, tmpl.Description, tmpl.Category, pq.Array(tmpl.Tags),
			tmpl.Image, tmpl.Author, tmpl.AuthorOrg, tmpl.Version,
			tmpl.Published, tmpl.Featured, tmpl.UseCount, resourcesJSON, networkJSON, envJSON, tmpl.Policy,
			packagesJSON, tmpl.Readme, tmpl.StartupScript,
		)
		if err != nil {
			return fmt.Errorf("insert built-in template %s: %w", tmpl.Slug, err)
		}
	}

	return nil
}
