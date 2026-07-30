package handlers

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/nickvd7/vaultrun/internal/nlpolicy"
)

type NLPolicyHandler struct {
	h        *Hub
	parser   nlpolicy.Parser
	compiler *nlpolicy.Compiler
}

func NewNLPolicyHandler(h *Hub) *NLPolicyHandler {
	var parser nlpolicy.Parser
	
	// Use OpenAI parser if API key is available, otherwise mock
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		parser = nlpolicy.NewOpenAIParser(apiKey)
		slog.Info("NL Policy: using OpenAI parser")
	} else {
		parser = nlpolicy.NewMockParser()
		slog.Warn("NL Policy: using mock parser (set OPENAI_API_KEY for real LLM parsing)")
	}

	return &NLPolicyHandler{
		h:        h,
		parser:   parser,
		compiler: nlpolicy.NewCompiler(),
	}
}

// POST /api/v1/policies/parse
// Parse natural language policy to structured JSON
func (ph *NLPolicyHandler) ParsePolicy(c *gin.Context) {
	var req struct {
		NaturalLanguage string `json:"natural_language" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	policy, err := ph.parser.Parse(c.Request.Context(), req.NaturalLanguage)
	if err != nil {
		slog.Error("policy parse failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse policy"})
		return
	}

	c.JSON(http.StatusOK, policy)
}

// POST /api/v1/policies/validate
// Validate natural language policy
func (ph *NLPolicyHandler) ValidatePolicy(c *gin.Context) {
	var req struct {
		NaturalLanguage string `json:"natural_language" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := ph.parser.Validate(c.Request.Context(), req.NaturalLanguage)
	if err != nil {
		slog.Error("policy validation failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "validation failed"})
		return
	}

	c.JSON(http.StatusOK, result)
}

// POST /api/v1/policies/compile
// Parse and compile policy to all formats
func (ph *NLPolicyHandler) CompilePolicy(c *gin.Context) {
	var req struct {
		NaturalLanguage string `json:"natural_language" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse
	policy, err := ph.parser.Parse(c.Request.Context(), req.NaturalLanguage)
	if err != nil {
		slog.Error("policy parse failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse policy"})
		return
	}

	// Compile
	compiled, err := ph.compiler.CompileAll(policy)
	if err != nil {
		slog.Error("policy compile failed", "err", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compile policy"})
		return
	}

	c.JSON(http.StatusOK, compiled)
}

// GET /api/v1/policies/templates
// List available policy templates
func (ph *NLPolicyHandler) ListTemplates(c *gin.Context) {
	category := c.Query("category")
	templates := nlpolicy.ListTemplates(category)
	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

// GET /api/v1/policies/templates/:name
// Get specific policy template
func (ph *NLPolicyHandler) GetTemplate(c *gin.Context) {
	name := c.Param("name")
	template := nlpolicy.GetTemplate(name)
	
	if template == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	c.JSON(http.StatusOK, template)
}

// POST /api/v1/policies/from-template/:name
// Generate policy from template
func (ph *NLPolicyHandler) FromTemplate(c *gin.Context) {
	name := c.Param("name")
	template := nlpolicy.GetTemplate(name)
	
	if template == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "template not found"})
		return
	}

	// Parse template
	policy, err := ph.parser.Parse(c.Request.Context(), template.Template)
	if err != nil {
		slog.Error("template parse failed", "err", err, "template", name)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse template"})
		return
	}

	// Compile
	compiled, err := ph.compiler.CompileAll(policy)
	if err != nil {
		slog.Error("template compile failed", "err", err, "template", name)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to compile template"})
		return
	}

	c.JSON(http.StatusOK, compiled)
}

// POST /api/v1/sessions (extended with NL policy support)
// This would be integrated into the existing session creation handler
// For now, we document how it would work:
//
// Request body can include:
// {
//   "image": "python:3.12-slim",
//   "name": "my-session",
//   "policy_natural_language": "Allow github.com and pypi.org, max 2 CPU, 4GB RAM"
// }
//
// The handler would:
// 1. Parse NL policy to structured policy
// 2. Compile to OPA/Docker configs
// 3. Create session with those configs
