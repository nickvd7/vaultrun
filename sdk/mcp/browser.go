// Browser automation MCP tools
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/nickvd7/vaultrun/internal/browser"
)

// Every tool here assembles a Playwright script and runs it through
// run_command, so each argument that reaches the script is either escaped for a
// Python string literal or checked against an allowlist. The helpers come from
// internal/browser rather than being restated: two copies of an escaping rule
// means one of them is eventually wrong.
//
// browserPolicy is the network policy applied to navigation targets. It is the
// secure default — private ranges, loopback and cloud metadata endpoints are
// refused — because the MCP server has no per-session policy to consult.
var browserPolicy = browser.DefaultNetworkPolicy()

// pythonScriptArgs packages a generated script as a run_command invocation.
func pythonScriptArgs(sessionID, script string) map[string]string {
	return map[string]string{
		"session_id": sessionID,
		"command":    "python3",
		"args":       fmt.Sprintf(`["-c", %s]`, jsonString(script)),
	}
}

// requireArg fetches a mandatory string argument.
func requireArg(args map[string]string, name string) (string, error) {
	v := args[name]
	if v == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return v, nil
}

// browserTimeout reads an optional millisecond timeout.
//
// The value is interpolated into the script as a bare Python integer, so it is
// parsed rather than escaped: a non-numeric argument is a caller error, and
// accepting one verbatim would put arbitrary text where Python expects an
// expression.
func browserTimeout(raw string) (int, error) {
	if raw == "" {
		return browser.DefaultBrowserTimeoutMs, nil
	}
	ms, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("timeout must be a whole number of milliseconds, got %q", raw)
	}
	return browser.ClampTimeout(ms), nil
}

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
						Enum:        []string{"load", "domcontentloaded", "networkidle", "commit"},
						Description: "Wait condition (default: load).",
					},
					"timeout": {
						Type:        "string",
						Description: "Timeout in milliseconds (default: 30000, max: 120000).",
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
						Description: "CSS selector to wait for (optional, waits for the timeout if omitted).",
					},
					"timeout": {
						Type:        "string",
						Description: "Timeout in milliseconds (default: 30000, max: 120000).",
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
						Type: "string",
						Enum: []string{
							"Letter", "Legal", "Tabloid", "Ledger",
							"A0", "A1", "A2", "A3", "A4", "A5", "A6",
						},
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
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	targetURL, err := requireArg(args, "url")
	if err != nil {
		return mcpToolResult{}, err
	}
	// Refuse the destination before a script exists to run it. A sandbox with
	// networking enabled can otherwise be pointed at the host's own services or
	// at the cloud metadata endpoint, which hands back IAM credentials.
	if err := browserPolicy.ValidateURL(targetURL); err != nil {
		return mcpToolResult{}, fmt.Errorf("invalid url: %w", err)
	}

	waitUntil, err := browser.ValidateWaitUntil(args["wait_until"])
	if err != nil {
		return mcpToolResult{}, err
	}

	timeout, err := browserTimeout(args["timeout"])
	if err != nil {
		return mcpToolResult{}, err
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.set_default_timeout(%d)
    page.goto('%s', wait_until='%s')
    print("Navigated")
    browser.close()
`, timeout, browser.EscapePythonString(targetURL), waitUntil)

	result, err := s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("navigation failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserScreenshot(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	outPath, err := browser.ValidateWorkspacePath(args["path"], "/workspace/screenshot.png")
	if err != nil {
		return mcpToolResult{}, err
	}

	fullPage := "False"
	if args["full_page"] == "true" {
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
`, browser.EscapePythonString(outPath), fullPage, browser.EscapePythonString(outPath))

	result, err := s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("screenshot failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserClick(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	selector, err := requireArg(args, "selector")
	if err != nil {
		return mcpToolResult{}, err
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.locator('%s').first.click()
    print("Clicked")
    browser.close()
`, browser.EscapePythonString(selector))

	result, err := s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("click failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserFill(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	selector, err := requireArg(args, "selector")
	if err != nil {
		return mcpToolResult{}, err
	}

	value, err := requireArg(args, "value")
	if err != nil {
		return mcpToolResult{}, err
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.locator('%s').first.fill('%s')
    print("Filled")
    browser.close()
`, browser.EscapePythonString(selector), browser.EscapePythonString(value))

	result, err := s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("fill failed: %w", err)
	}

	return result, nil
}

func (s *server) toolBrowserExtract(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	extractType := args["extract"]
	if extractType == "" {
		extractType = "text"
	}
	if extractType != "text" && extractType != "html" {
		return mcpToolResult{}, fmt.Errorf("invalid extract %q: must be one of text, html", extractType)
	}

	var extractCode string
	switch selector := args["selector"]; {
	case selector == "":
		extractCode = `content = page.content()`
	case extractType == "html":
		extractCode = fmt.Sprintf(`content = page.locator('%s').first.inner_html()`,
			browser.EscapePythonString(selector))
	default:
		extractCode = fmt.Sprintf(`content = page.locator('%s').first.text_content()`,
			browser.EscapePythonString(selector))
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

	return s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
}

func (s *server) toolBrowserEvaluate(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	jsScript, err := requireArg(args, "script")
	if err != nil {
		return mcpToolResult{}, err
	}

	// JavaScript is free-form text that would have to survive both a Python
	// literal and the shell-free exec boundary. Base64 keeps it out of the
	// generated source entirely: the script carries only characters from the
	// base64 alphabet, so no input can terminate the literal that holds it.
	encoded := base64.StdEncoding.EncodeToString([]byte(jsScript))

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright
import base64
import json

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    js_code = base64.b64decode('%s').decode('utf-8')
    result = page.evaluate(js_code)
    print(json.dumps(result))
    browser.close()
`, encoded)

	return s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
}

func (s *server) toolBrowserWait(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	timeout, err := browserTimeout(args["timeout"])
	if err != nil {
		return mcpToolResult{}, err
	}

	var waitCode string
	if selector := args["selector"]; selector != "" {
		waitCode = fmt.Sprintf(`page.wait_for_selector('%s', timeout=%d)`,
			browser.EscapePythonString(selector), timeout)
	} else {
		waitCode = fmt.Sprintf(`page.wait_for_timeout(%d)`, timeout)
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

	return s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
}

func (s *server) toolBrowserPDF(ctx context.Context, args map[string]string) (mcpToolResult, error) {
	sessionID, err := requireArg(args, "session_id")
	if err != nil {
		return mcpToolResult{}, err
	}

	outPath, err := browser.ValidateWorkspacePath(args["path"], "/workspace/page.pdf")
	if err != nil {
		return mcpToolResult{}, err
	}

	format, err := browser.ValidatePaperFormat(args["format"])
	if err != nil {
		return mcpToolResult{}, err
	}

	script := fmt.Sprintf(`
from playwright.sync_api import sync_playwright

with sync_playwright() as p:
    browser = p.chromium.launch(headless=True)
    page = browser.new_page()
    page.pdf(path='%s', format='%s')
    print("PDF saved: %s")
    browser.close()
`, browser.EscapePythonString(outPath), format, browser.EscapePythonString(outPath))

	result, err := s.toolRunCommand(ctx, pythonScriptArgs(sessionID, script))
	if err != nil {
		return mcpToolResult{}, fmt.Errorf("PDF generation failed: %w", err)
	}

	return result, nil
}

// jsonString quotes s as a JSON string, for embedding in the run_command args
// array.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
