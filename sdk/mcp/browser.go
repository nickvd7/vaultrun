// Browser automation MCP tools
package main

import (
	"context"
	"encoding/json"
	"fmt"
)

// Browser tool definitions
func browserToolDefinitions() []mcpTool {
	return []mcpTool{
		{
			Name: "browser_navigate",
			Description: "Navigate to a URL in the browser using Playwright. Waits for page load. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"url": {
						Type:        "string",
						Description: "URL to navigate to (required, http/https only).",
					},
					"wait_until": {
						Type:        "string",
						Enum:        []string{"load", "domcontentloaded", "networkidle"},
						Description: "Wait condition (default: load).",
					},
				},
				Required: []string{"session_id", "url"},
			},
		},
		{
			Name: "browser_screenshot",
			Description: "Take a screenshot of the current page. Returns path to screenshot artifact. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"path": {
						Type:        "string",
						Description: "Output path in /workspace (default: /workspace/screenshot.png).",
					},
					"full_page": {
						Type:        "string",
						Enum:        []string{"true", "false"},
						Description: "Capture full page (true) or viewport only (false, default).",
					},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "browser_click",
			Description: "Click an element on the page. Waits for element to be visible. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"selector": {
						Type:        "string",
						Description: "CSS selector for element to click (required).",
					},
				},
				Required: []string{"session_id", "selector"},
			},
		},
		{
			Name: "browser_fill",
			Description: "Fill an input field with text. Clears existing value first. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"selector": {
						Type:        "string",
						Description: "CSS selector for input element (required).",
					},
					"value": {
						Type:        "string",
						Description: "Text value to fill (required).",
					},
				},
				Required: []string{"session_id", "selector", "value"},
			},
		},
		{
			Name: "browser_extract",
			Description: "Extract text or HTML from the page. Returns extracted content. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"selector": {
						Type:        "string",
						Description: "CSS selector to extract from (optional, extracts entire page if omitted).",
					},
					"extract": {
						Type:        "string",
						Enum:        []string{"text", "html"},
						Description: "What to extract: 'text' or 'html' (default: text).",
					},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "browser_evaluate",
			Description: "Execute JavaScript in the browser context. Returns the result. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"script": {
						Type:        "string",
						Description: "JavaScript code to execute (required).",
					},
				},
				Required: []string{"session_id", "script"},
			},
		},
		{
			Name: "browser_wait",
			Description: "Wait for an element to appear or a fixed timeout. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"selector": {
						Type:        "string",
						Description: "CSS selector to wait for (optional, waits 1s if omitted).",
					},
					"timeout": {
						Type:        "string",
						Description: "Timeout in milliseconds (default: 30000).",
					},
				},
				Required: []string{"session_id"},
			},
		},
		{
			Name: "browser_pdf",
			Description: "Generate a PDF of the current page. Returns path to PDF artifact. " +
				"Session must use a browser image (vaultrun/browser:playwright-python).",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"session_id": {
						Type:        "string",
						Description: "Session ID (required).",
					},
					"path": {
						Type:        "string",
						Description: "Output path in /workspace (default: /workspace/page.pdf).",
					},
					"format": {
						Type:        "string",
						Enum:        []string{"A4", "Letter"},
						Description: "Page format (default: A4).",
					},
				},
				Required: []string{"session_id"},
			},
		},
	}
}

// Browser tool implementations
func (s *server) toolBrowserNavigate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID := args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	url := args["url"]
	if url == "" {
		return mcpToolResult{}, fmt.Errorf("url is required")
	}

	waitUntil := "load"
	if wu := args["wait_until"]; wu != "" {
		waitUntil = wu
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.goto('%s', wait_until='%s')
    print("Navigated to: %s")
    browser.close()
`, escapeString(url), waitUntil, url)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	result, err := s.toolRunCommand(ctx, cmdArgs)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("navigation failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserScreenshot(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	path := "/workspace/screenshot.png"
	if p := args["path"]; p != "" {
		path = p
	}

	fullPage := "False"
	if fp := args["full_page"]; fp == "true" {
		fullPage = "True"
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.screenshot(path='%s', full_page=%s)
    print("Screenshot saved: %s")
    browser.close()
`, path, fullPage, path)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	result, err := s.toolRunCommand(ctx, cmdArgs)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("screenshot failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserClick(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	selector:= args["selector"]
	if selector == "" {
		return mcpToolResult{}, fmt.Errorf("selector is required")
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.locator('%s').first.click()
    print("Clicked: %s")
    browser.close()
`, escapeString(selector), selector)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	result, err := s.toolRunCommand(ctx, cmdArgs)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("click failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserFill(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	selector:= args["selector"]
	if selector == "" {
		return mcpToolResult{}, fmt.Errorf("selector is required")
	}

	value:= args["value"]
	if !ok {
		return mcpToolResult{}, fmt.Errorf("value is required")
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.locator('%s').first.fill('%s')
    print("Filled: %s")
    browser.close()
`, escapeString(selector), escapeString(value), selector)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	result, err := s.toolRunCommand(ctx, cmdArgs)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("fill failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserExtract(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	selector := ""
	if sel := args["selector"]; sel != "" {
		selector = sel
	}

	extractType := "text"
	if et := args["extract"]; et != "" {
		extractType = et
	}

	var extractCode string
	if selector != "" {
		if extractType == "html" {
			extractCode = fmt.Sprintf(`content = page.locator('%s').first.inner_html()`, escapeString(selector))
		} else {
			extractCode = fmt.Sprintf(`content = page.locator('%s').first.text_content()`, escapeString(selector))
		}
	} else {
		extractCode = `content = page.content()`
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    %s
    print(content)
    browser.close()
`, extractCode)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	return s.toolRunCommand(ctx, cmdArgs)
}

func (s *server) toolBrowserEvaluate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	jsScript:= args["script"]
	if jsScript == "" {
		return mcpToolResult{}, fmt.Errorf("script is required")
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import json

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    result = page.evaluate('''%s''')
    print(json.dumps(result))
    browser.close()
`, escapeString(jsScript))

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	return s.toolRunCommand(ctx, cmdArgs)
}

func (s *server) toolBrowserWait(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	selector := ""
	if sel := args["selector"]; sel != "" {
		selector = sel
	}

	timeout := "30000"
	if to := args["timeout"]; to != "" {
		timeout = to
	}

	var waitCode string
	if selector != "" {
		waitCode = fmt.Sprintf(`page.wait_for_selector('%s', timeout=%s)`, escapeString(selector), timeout)
	} else {
		waitCode = `page.wait_for_timeout(1000)`
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    %s
    print("Wait complete")
    browser.close()
`, waitCode)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	return s.toolRunCommand(ctx, cmdArgs)
}

func (s *server) toolBrowserPDF(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID:= args["session_id"]
	if sessionID == "" {
		return mcpToolResult{}, fmt.Errorf("session_id is required")
	}

	path := "/workspace/page.pdf"
	if p := args["path"]; p != "" {
		path = p
	}

	format := "A4"
	if f := args["format"]; f != "" {
		format = f
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.pdf(path='%s', format='%s')
    print("PDF saved: %s")
    browser.close()
`, path, format, path)

	cmdArgs := map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}

	result, err := s.toolRunCommand(ctx, cmdArgs)
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("PDF generation failed: %w", err)
	}

	return result, nil
}

// Helper: escape single quotes for Python strings
func escapeString(s string) string {
	s = jsonEscape(s)
	return s
}

// Helper: escape and quote string as JSON
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Helper: JSON escape
func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	// Remove surrounding quotes
	if len(b) >= 2 {
		return string(b[1 : len(b)-1])
	}
	return string(b)
}
