// MCP Apps extension — server-rendered UI resources for hosts that support it.
package main

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	sessionPanelURI   = "ui://vaultrun/session-panel"
	runPanelURI       = "ui://vaultrun/run-panel"
	artifactsPanelURI = "ui://vaultrun/artifacts-panel"
)

const appPanelCSS = `
  :root { color-scheme: dark; --bg:#0b1220; --fg:#e8eef8; --muted:#8b9bb4; --accent:#3d8bfd; --line:#1e2a3d; }
  body { margin:0; font:14px/1.45 ui-sans-serif, system-ui, sans-serif; background:var(--bg); color:var(--fg); padding:16px; }
  h1 { font-size:16px; margin:0 0 8px; letter-spacing:.02em; }
  p { margin:0 0 12px; color:var(--muted); }
  ul { margin:0 0 12px; padding-left:18px; color:var(--muted); }
  li { margin:0 0 6px; }
  .hint { border-top:1px solid var(--line); padding-top:12px; color:var(--muted); font-size:12px; }
  code { color:var(--accent); }
`

const sessionPanelHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>VaultRun Sessions</title>
<style>` + appPanelCSS + `</style>
</head>
<body>
  <h1>VaultRun sessions</h1>
  <p>Use <code>list_sessions</code> / <code>get_session</code> / <code>create_session</code> to manage sandboxes. Pass an explicit <code>session_id</code> on every follow-up tool call — the MCP transport is stateless.</p>
  <div class="hint">MCP Apps surface <code>ui://vaultrun/session-panel</code>. Disable all Apps with <code>MCP_APPS_ENABLED=false</code>.</div>
</body>
</html>`

const runPanelHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>VaultRun Runs</title>
<style>` + appPanelCSS + `</style>
</head>
<body>
  <h1>VaultRun runs</h1>
  <p>Execute work with <code>run_command</code> (optionally <code>async=true</code> / <code>confirm=true</code>), then inspect results with <code>get_run</code>.</p>
  <ul>
    <li>Keep the returned <code>session_id</code> for follow-up commands.</li>
    <li>For long jobs poll <code>tasks/get</code>; handle <code>input_required</code> via <code>tasks/update</code>.</li>
  </ul>
  <div class="hint">MCP Apps surface <code>ui://vaultrun/run-panel</code>.</div>
</body>
</html>`

const artifactsPanelHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>VaultRun Artifacts</title>
<style>` + appPanelCSS + `</style>
</head>
<body>
  <h1>VaultRun artifacts</h1>
  <p>Shared outputs live outside any single sandbox. Use <code>list_artifacts</code> to discover them, then download or attach paths in later tool calls.</p>
  <div class="hint">MCP Apps surface <code>ui://vaultrun/artifacts-panel</code>.</div>
</body>
</html>`

type appsPanel struct {
	uri, name, description, html string
}

func appsPanels() []appsPanel {
	return []appsPanel{
		{sessionPanelURI, "VaultRun session panel", "UI explaining VaultRun session handles.", sessionPanelHTML},
		{runPanelURI, "VaultRun run panel", "UI for run_command / get_run workflows and Tasks polling.", runPanelHTML},
		{artifactsPanelURI, "VaultRun artifacts panel", "UI for shared artifact discovery.", artifactsPanelHTML},
	}
}

func registerAppsResources(sdk *mcpsdk.Server) {
	panels := appsPanels()
	byURI := make(map[string]appsPanel, len(panels))
	for _, p := range panels {
		byURI[p.uri] = p
		p := p
		sdk.AddResource(&mcpsdk.Resource{
			URI:         p.uri,
			Name:        p.name,
			Description: p.description,
			MIMEType:    "text/html;profile=mcp-app",
		}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			uri := p.uri
			if req != nil && req.Params != nil && req.Params.URI != "" {
				uri = req.Params.URI
			}
			panel, ok := byURI[uri]
			if !ok {
				return nil, mcpsdk.ResourceNotFoundError(uri)
			}
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{{
					URI:      panel.uri,
					MIMEType: "text/html;profile=mcp-app",
					Text:     panel.html,
				}},
			}, nil
		})
	}
}

// toolUIMeta attaches an Apps UI resource URI for selected tools.
func toolUIMeta(name string) mcpsdk.Meta {
	var uri string
	switch name {
	case "list_sessions", "get_session", "create_session":
		uri = sessionPanelURI
	case "run_command", "get_run":
		uri = runPanelURI
	case "list_artifacts":
		uri = artifactsPanelURI
	default:
		return nil
	}
	return mcpsdk.Meta{
		"ui": map[string]any{
			"resourceUri": uri,
		},
	}
}
