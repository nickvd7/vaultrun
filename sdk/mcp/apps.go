// MCP Apps extension — server-rendered UI resource for hosts that support it.
package main

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const sessionPanelURI = "ui://vaultrun/session-panel"

const sessionPanelHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>VaultRun Sessions</title>
<style>
  :root { color-scheme: dark; --bg:#0b1220; --fg:#e8eef8; --muted:#8b9bb4; --accent:#3d8bfd; --line:#1e2a3d; }
  body { margin:0; font:14px/1.45 ui-sans-serif, system-ui, sans-serif; background:var(--bg); color:var(--fg); padding:16px; }
  h1 { font-size:16px; margin:0 0 8px; letter-spacing:.02em; }
  p { margin:0 0 12px; color:var(--muted); }
  .hint { border-top:1px solid var(--line); padding-top:12px; color:var(--muted); font-size:12px; }
  code { color:var(--accent); }
</style>
</head>
<body>
  <h1>VaultRun</h1>
  <p>Use <code>list_sessions</code> / <code>get_session</code> to inspect sandboxes. Pass explicit <code>session_id</code> handles between tools — the MCP transport is stateless.</p>
  <div class="hint">This panel is an MCP Apps surface (<code>ui://vaultrun/session-panel</code>). Hosts without Apps support ignore it and keep using tool text.</div>
</body>
</html>`

func registerAppsResources(sdk *mcpsdk.Server) {
	sdk.AddResource(&mcpsdk.Resource{
		URI:         sessionPanelURI,
		Name:        "VaultRun session panel",
		Description: "Lightweight MCP Apps UI explaining VaultRun session handles.",
		MIMEType:    "text/html;profile=mcp-app",
	}, func(ctx context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		uri := sessionPanelURI
		if req != nil && req.Params != nil && req.Params.URI != "" {
			uri = req.Params.URI
		}
		if uri != sessionPanelURI {
			return nil, mcpsdk.ResourceNotFoundError(uri)
		}
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{{
				URI:      sessionPanelURI,
				MIMEType: "text/html;profile=mcp-app",
				Text:     sessionPanelHTML,
			}},
		}, nil
	})
}

// toolUIMeta attaches an Apps UI resource URI for selected tools.
func toolUIMeta(name string) mcpsdk.Meta {
	switch name {
	case "list_sessions", "get_session", "create_session":
		return mcpsdk.Meta{
			"ui": map[string]any{
				"resourceUri": sessionPanelURI,
			},
		}
	default:
		return nil
	}
}
