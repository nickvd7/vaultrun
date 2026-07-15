import Link from "next/link";
import {
  Shield,
  Terminal,
  Globe,
  Lock,
  ScrollText,
  Zap,
  ArrowRight,
  Box,
  Cpu,
  Database,
  Cloud,
  GitPullRequest,
  FolderOpen,
  ChevronRight,
  KeyRound,
  Users,
} from "lucide-react";

const features = [
  {
    icon: Box,
    title: "Isolated Docker Sandboxes",
    desc: "Every session runs in its own container with dropped Linux capabilities, seccomp filtering, and zero host access.",
  },
  {
    icon: Shield,
    title: "Zero-Trust Security",
    desc: "API key auth, path-traversal prevention, non-root execution, optional network isolation, and HMAC-signed audit logs.",
  },
  {
    icon: Globe,
    title: "MCP-Native (53 tools)",
    desc: "Built-in Model Context Protocol server exposes 53 tools over stdio or HTTP — works with Claude, OpenAI, OpenRouter, and any MCP client.",
  },
  {
    icon: KeyRound,
    title: "Enterprise SSO (OIDC & SAML)",
    desc: "Federate with Okta, Azure AD, Google Workspace, or any OIDC/SAML 2.0 IdP — PKCE, signed assertions, server-side session revocation, and auto-provisioned API keys.",
  },
  {
    icon: Users,
    title: "Multi-Tenant Orgs & RBAC",
    desc: "Group sessions under organizations with viewer/executor/admin roles — share sandboxes across a team without handing out the master key.",
  },
  {
    icon: Cloud,
    title: "AWS Integrations",
    desc: "Native tools for S3, SSM Parameter Store, Secrets Manager, and Lambda — with explicit opt-in and audit logging for every call.",
  },
  {
    icon: Database,
    title: "Database Access",
    desc: "Query and mutate SQLite, PostgreSQL, and MongoDB directly from your agent. Generate Mongoose schemas from live collections.",
  },
  {
    icon: GitPullRequest,
    title: "GitHub CI Runner",
    desc: "Webhook-driven CI that runs your PR test suite inside a VaultRun sandbox and posts results back with Slack and Teams notifications.",
  },
  {
    icon: FolderOpen,
    title: "Filesystem & S3 Artifacts",
    desc: "Scoped filesystem access with allowlist, S3 artifact storage with presigned URLs, and a local fallback — no extra infrastructure needed.",
  },
  {
    icon: Lock,
    title: "Fully Self-Hosted",
    desc: "Your code, your data, your infrastructure. No telemetry, no SaaS dependency, no data leaving your network.",
  },
  {
    icon: ScrollText,
    title: "Immutable Audit Trail",
    desc: "Every action is logged with HMAC signatures — searchable, tamper-evident, and exportable to your SIEM.",
  },
  {
    icon: Zap,
    title: "Fast & Scalable",
    desc: "Warm container pool, async job queues via Redis, and Prometheus metrics — ready for production workloads.",
  },
];

const toolGroups = [
  {
    label: "Sandbox",
    tools: ["create_session", "run_command", "upload_file", "read_file", "list_files", "delete_file", "get_run", "+more"],
  },
  {
    label: "Snapshots & Artifacts",
    tools: ["create_snapshot", "list_snapshots", "create_artifact", "list_artifacts", "list_audit_logs"],
  },
  {
    label: "Docker & GitHub",
    tools: ["list_images", "pull_image", "run_github_repo", "github_post_comment"],
  },
  {
    label: "Filesystem",
    tools: ["fs_read_file", "fs_write_file", "fs_list_dir", "fs_delete_file"],
  },
  {
    label: "AWS",
    tools: ["s3_put_object", "ssm_get_parameter", "sm_get_secret", "lambda_invoke", "+more"],
  },
  {
    label: "Databases",
    tools: ["sqlite_query", "pg_query", "mongo_find", "mongo_generate_mongoose", "+more"],
  },
];

const mcpConfig = `{
  "mcpServers": {
    "vaultrun": {
      "command": "vaultrun-mcp",
      "env": {
        "VAULTRUN_BASE_URL": "http://localhost:8080",
        "VAULTRUN_API_KEY": "vr_your_key"
      }
    }
  }
}`;

const mcpHTTPConfig = `# HTTP transport (for OpenAI, OpenRouter, custom agents)
MCP_TRANSPORT=http \\
MCP_AUTH_TOKEN=your-secret \\
VAULTRUN_BASE_URL=http://localhost:8080 \\
VAULTRUN_API_KEY=vr_your_key \\
./vaultrun-mcp

# POST /mcp — JSON-RPC 2.0
# Authorization: Bearer your-secret`;

const ssoConfig = `# OIDC — Okta, Azure AD, Google Workspace, Keycloak, Auth0
OIDC_ISSUER_URL=https://your-tenant.okta.com
OIDC_CLIENT_ID=...
OIDC_CLIENT_SECRET=...
SSO_SESSION_SECRET=$(openssl rand -hex 32)

# SAML 2.0 — Okta, AD FS, OneLogin, …
SAML_IDP_METADATA_URL=https://idp.example.com/metadata
SAML_SP_CERT_FILE=./certs/sp.crt
SAML_SP_KEY_FILE=./certs/sp.key

# → /auth/oidc/login or /auth/saml/login`;

const ciConfig = `# GitHub webhook → VaultRun sandbox → PR comment
GITHUB_TOKEN=ghp_... \\
GITHUB_WEBHOOK_SECRET=your-secret \\
VAULTRUN_BASE_URL=http://vaultrun \\
VAULTRUN_API_KEY=vr_... \\
SLACK_WEBHOOK_URL=https://hooks.slack.com/... \\
./ci-runner`;

export default function LandingPage() {
  return (
    <div className="min-h-screen bg-[#0a0a0f] text-slate-200 flex flex-col">
      {/* Nav */}
      <header className="border-b border-slate-800/60 sticky top-0 z-10 bg-[#0a0a0f]/90 backdrop-blur">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Shield className="w-5 h-5 text-indigo-400" />
            <span className="font-semibold text-slate-100 tracking-tight">VaultRun</span>
            <span className="ml-2 text-xs px-2 py-0.5 rounded-full bg-indigo-900/40 border border-indigo-700/30 text-indigo-300 font-mono">v0.2.1</span>
          </div>
          <div className="flex items-center gap-3">
            <a
              href="https://github.com/nickvd7/vaultrun"
              target="_blank"
              rel="noopener noreferrer"
              className="hidden sm:flex items-center gap-1.5 px-3 py-1.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 hover:text-slate-200 text-sm transition-colors"
            >
              GitHub
            </a>
            <Link
              href="/dashboard"
              className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-sm font-medium text-white transition-colors"
            >
              Open Dashboard <ArrowRight className="w-3.5 h-3.5" />
            </Link>
          </div>
        </div>
      </header>

      {/* Hero */}
      <section className="max-w-6xl mx-auto px-6 pt-24 pb-16 text-center">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full bg-indigo-900/30 border border-indigo-700/40 text-indigo-300 text-xs font-medium mb-8">
          <Terminal className="w-3.5 h-3.5" />
          Self-hosted · Open source · No SaaS
        </div>
        <h1 className="text-5xl font-bold tracking-tight text-slate-100 mb-6 leading-tight">
          Secure sandbox runtime
          <br />
          <span className="text-indigo-400">for AI agents</span>
        </h1>
        <p className="text-xl text-slate-400 max-w-2xl mx-auto mb-10 leading-relaxed">
          VaultRun gives your AI agents isolated Docker containers to execute code,
          query databases, call cloud APIs, and manage files — safely, on your own infrastructure.
        </p>
        <div className="flex items-center justify-center gap-4 flex-wrap">
          <Link
            href="/dashboard"
            className="flex items-center gap-2 px-6 py-3 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white font-medium transition-colors"
          >
            Open Dashboard <ArrowRight className="w-4 h-4" />
          </Link>
          <a
            href="https://github.com/nickvd7/vaultrun"
            target="_blank"
            rel="noopener noreferrer"
            className="px-6 py-3 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 hover:text-slate-100 font-medium transition-colors"
          >
            View on GitHub
          </a>
        </div>

        {/* Stats row */}
        <div className="mt-16 grid grid-cols-2 sm:grid-cols-4 gap-4 max-w-3xl mx-auto">
          {[
            { value: "53", label: "MCP tools" },
            { value: "2", label: "transports (stdio + HTTP)" },
            { value: "3", label: "database backends" },
            { value: "4", label: "AWS services" },
          ].map(({ value, label }) => (
            <div key={label} className="bg-[#0f0f1a] border border-slate-800 rounded-xl p-4 text-center">
              <div className="text-2xl font-bold text-indigo-400">{value}</div>
              <div className="text-xs text-slate-500 mt-1">{label}</div>
            </div>
          ))}
        </div>
      </section>

      {/* Features grid */}
      <section className="max-w-6xl mx-auto px-6 pb-20">
        <h2 className="text-2xl font-semibold text-slate-100 text-center mb-10">
          Everything you need for secure agent execution
        </h2>
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5">
          {features.map(({ icon: Icon, title, desc }) => (
            <div
              key={title}
              className="bg-[#0f0f1a] border border-slate-800 rounded-xl p-6 hover:border-slate-700 transition-colors"
            >
              <div className="flex items-center gap-3 mb-3">
                <div className="flex items-center justify-center w-9 h-9 rounded-lg bg-indigo-900/40 border border-indigo-700/30">
                  <Icon className="w-4 h-4 text-indigo-400" />
                </div>
                <h3 className="font-medium text-slate-100 text-sm">{title}</h3>
              </div>
              <p className="text-slate-500 text-sm leading-relaxed">{desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* MCP tools overview */}
      <section className="max-w-6xl mx-auto px-6 pb-20">
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-2xl p-8 md:p-10">
          <div className="flex items-center gap-2 mb-2">
            <Cpu className="w-4 h-4 text-indigo-400" />
            <span className="text-xs uppercase tracking-widest text-indigo-400 font-medium">53 MCP tools</span>
          </div>
          <h2 className="text-xl font-semibold text-slate-100 mb-2">All capabilities, one MCP server</h2>
          <p className="text-slate-400 text-sm mb-8 max-w-2xl">
            Every VaultRun feature is exposed as an MCP tool — your agent can orchestrate sandboxes,
            query databases, manage cloud resources, and trigger CI without writing any integration code.
          </p>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
            {toolGroups.map(({ label, tools }) => (
              <div key={label} className="bg-[#07070d] border border-slate-800 rounded-xl p-4">
                <div className="text-xs font-medium text-indigo-300 uppercase tracking-widest mb-3">{label}</div>
                <ul className="space-y-1.5">
                  {tools.map((t) => (
                    <li key={t} className="flex items-center gap-2 text-xs text-slate-400 font-mono">
                      <ChevronRight className="w-3 h-3 text-slate-700 shrink-0" />
                      {t}
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* MCP integration */}
      <section className="max-w-6xl mx-auto px-6 pb-20">
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-2xl p-8 md:p-12">
          <div className="grid md:grid-cols-2 gap-10 items-start">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <Globe className="w-5 h-5 text-indigo-400" />
                <span className="text-xs uppercase tracking-widest text-indigo-400 font-medium">MCP integration</span>
              </div>
              <h2 className="text-2xl font-semibold text-slate-100 mb-4">
                Works with any MCP-compatible AI
              </h2>
              <p className="text-slate-400 text-sm leading-relaxed mb-4">
                Add VaultRun to <strong className="text-slate-300">Claude Desktop</strong>, <strong className="text-slate-300">Claude Code</strong>,
                or any platform that supports the Model Context Protocol — via stdio for Claude, or HTTP for OpenAI, OpenRouter, and custom agents.
              </p>
              <ul className="space-y-2 mb-6">
                {[
                  "stdio transport — zero config for Claude Desktop / Claude Code",
                  "HTTP transport — JSON-RPC 2.0 over POST /mcp",
                  "Bearer token auth + per-IP rate limiting",
                  "TLS via Let's Encrypt or static cert",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2 text-sm text-slate-400">
                    <ChevronRight className="w-4 h-4 text-indigo-500 shrink-0 mt-0.5" />
                    {item}
                  </li>
                ))}
              </ul>
              <Link
                href="/dashboard"
                className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
              >
                Open Dashboard <ArrowRight className="w-3.5 h-3.5" />
              </Link>
            </div>
            <div className="space-y-4">
              <div>
                <div className="flex items-center gap-2 mb-2">
                  <div className="flex gap-1.5">
                    <div className="w-3 h-3 rounded-full bg-red-500/60" />
                    <div className="w-3 h-3 rounded-full bg-yellow-500/60" />
                    <div className="w-3 h-3 rounded-full bg-green-500/60" />
                  </div>
                  <span className="text-xs text-slate-600 font-mono">claude_desktop_config.json</span>
                </div>
                <pre className="bg-[#07070d] border border-slate-800 rounded-xl p-5 text-xs font-mono text-slate-300 overflow-x-auto leading-relaxed">
                  {mcpConfig}
                </pre>
              </div>
              <div>
                <div className="flex items-center gap-2 mb-2">
                  <div className="flex gap-1.5">
                    <div className="w-3 h-3 rounded-full bg-red-500/60" />
                    <div className="w-3 h-3 rounded-full bg-yellow-500/60" />
                    <div className="w-3 h-3 rounded-full bg-green-500/60" />
                  </div>
                  <span className="text-xs text-slate-600 font-mono">HTTP transport (OpenAI / OpenRouter)</span>
                </div>
                <pre className="bg-[#07070d] border border-slate-800 rounded-xl p-5 text-xs font-mono text-slate-300 overflow-x-auto leading-relaxed">
                  {mcpHTTPConfig}
                </pre>
              </div>
            </div>
          </div>
        </div>
      </section>

      {/* Enterprise SSO */}
      <section className="max-w-6xl mx-auto px-6 pb-20">
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-2xl p-8 md:p-12">
          <div className="grid md:grid-cols-2 gap-10 items-start">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <KeyRound className="w-5 h-5 text-indigo-400" />
                <span className="text-xs uppercase tracking-widest text-indigo-400 font-medium">Identity federation</span>
              </div>
              <h2 className="text-2xl font-semibold text-slate-100 mb-4">
                Bring your own identity provider
              </h2>
              <p className="text-slate-400 text-sm leading-relaxed mb-4">
                Let your team sign in to the dashboard with the IdP they already use.
                VaultRun speaks both <strong className="text-slate-300">OpenID Connect</strong> (Authorization Code + PKCE)
                and <strong className="text-slate-300">SAML 2.0</strong> — auto-provisioning a scoped
                API key and a signed session on first login. No master-key sharing required.
              </p>
              <ul className="space-y-2 mb-6">
                {[
                  "Okta, Azure AD, Google Workspace, Keycloak, Auth0, AD FS, OneLogin",
                  "PKCE + state/nonce validation, XML signature verification, replay protection",
                  "HttpOnly + Secure session cookies with server-side revocation on logout",
                  "Org-aware RBAC: map IdP groups to viewer / executor / admin roles",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2 text-sm text-slate-400">
                    <ChevronRight className="w-4 h-4 text-indigo-500 shrink-0 mt-0.5" />
                    {item}
                  </li>
                ))}
              </ul>
              <div className="flex flex-wrap gap-3">
                <a
                  href="https://vaultrun.dev/#enterprise"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
                >
                  Get Enterprise <ArrowRight className="w-3.5 h-3.5" />
                </a>
                <a
                  href="https://vaultrun.dev/enterprise.html"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-300 text-sm font-medium transition-colors"
                >
                  Procurement one-pager
                </a>
                <a
                  href="https://github.com/nickvd7/vaultrun/blob/main/docs/sso-setup.md"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg border border-slate-700 hover:border-slate-600 text-slate-400 text-sm transition-colors"
                >
                  SSO setup guide
                </a>
              </div>
            </div>
            <div>
              <div className="flex items-center gap-2 mb-2">
                <div className="flex gap-1.5">
                  <div className="w-3 h-3 rounded-full bg-red-500/60" />
                  <div className="w-3 h-3 rounded-full bg-yellow-500/60" />
                  <div className="w-3 h-3 rounded-full bg-green-500/60" />
                </div>
                <span className="text-xs text-slate-600 font-mono">.env — SSO configuration</span>
              </div>
              <pre className="bg-[#07070d] border border-slate-800 rounded-xl p-5 text-xs font-mono text-slate-300 overflow-x-auto leading-relaxed">
                {ssoConfig}
              </pre>
            </div>
          </div>
        </div>
      </section>

      {/* CI Runner */}
      <section className="max-w-6xl mx-auto px-6 pb-20">
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-2xl p-8 md:p-12">
          <div className="grid md:grid-cols-2 gap-10 items-start">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <GitPullRequest className="w-5 h-5 text-indigo-400" />
                <span className="text-xs uppercase tracking-widest text-indigo-400 font-medium">CI Runner</span>
              </div>
              <h2 className="text-2xl font-semibold text-slate-100 mb-4">
                GitHub CI in a VaultRun sandbox
              </h2>
              <p className="text-slate-400 text-sm leading-relaxed mb-4">
                Point a GitHub webhook at the CI runner and every PR automatically runs your test suite
                inside an isolated VaultRun sandbox. Results are posted back as a PR comment and commit
                status, with optional Slack and Teams notifications.
              </p>
              <ul className="space-y-2">
                {[
                  "HMAC-SHA256 webhook validation",
                  "Configurable test commands (JSON array)",
                  "Token-safe git clone via http.extraheader",
                  "Slack Block Kit + Teams Adaptive Card notifications",
                  "NOTIFY_ON_SUCCESS=false to suppress noise on green",
                ].map((item) => (
                  <li key={item} className="flex items-start gap-2 text-sm text-slate-400">
                    <ChevronRight className="w-4 h-4 text-indigo-500 shrink-0 mt-0.5" />
                    {item}
                  </li>
                ))}
              </ul>
            </div>
            <div>
              <div className="flex items-center gap-2 mb-2">
                <div className="flex gap-1.5">
                  <div className="w-3 h-3 rounded-full bg-red-500/60" />
                  <div className="w-3 h-3 rounded-full bg-yellow-500/60" />
                  <div className="w-3 h-3 rounded-full bg-green-500/60" />
                </div>
                <span className="text-xs text-slate-600 font-mono">ci-runner env</span>
              </div>
              <pre className="bg-[#07070d] border border-slate-800 rounded-xl p-5 text-xs font-mono text-slate-300 overflow-x-auto leading-relaxed">
                {ciConfig}
              </pre>
            </div>
          </div>
        </div>
      </section>

      {/* Quick start */}
      <section className="max-w-6xl mx-auto px-6 pb-24">
        <div className="text-center mb-10">
          <h2 className="text-2xl font-semibold text-slate-100 mb-3">Get started in minutes</h2>
          <p className="text-slate-400 text-sm">Prerequisites: Docker, Docker Compose, Go 1.23+</p>
        </div>
        <div className="grid md:grid-cols-3 gap-4">
          {[
            {
              step: "1",
              title: "Clone & configure",
              code: "git clone https://github.com/nickvd7/vaultrun\ncd vaultrun && cp .env.example .env\n# Set MASTER_API_KEY in .env",
            },
            {
              step: "2",
              title: "Start the stack",
              code: "make up\n# Starts API, Postgres, Redis,\n# and the dashboard at :3000",
            },
            {
              step: "3",
              title: "Create an API key",
              code: "make bootstrap-key\n# Use the key with the CLI, SDK,\n# or the MCP server",
            },
          ].map(({ step, title, code }) => (
            <div key={step} className="bg-[#0f0f1a] border border-slate-800 rounded-xl p-6">
              <div className="flex items-center gap-3 mb-4">
                <div className="w-7 h-7 rounded-full bg-indigo-600 flex items-center justify-center text-xs font-bold text-white shrink-0">
                  {step}
                </div>
                <span className="font-medium text-slate-200 text-sm">{title}</span>
              </div>
              <pre className="text-xs font-mono text-slate-400 leading-relaxed whitespace-pre-wrap">{code}</pre>
            </div>
          ))}
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-slate-800/60 py-8">
        <div className="max-w-6xl mx-auto px-6 flex flex-col sm:flex-row items-center justify-between gap-4 text-xs text-slate-600">
          <div className="flex items-center gap-2">
            <Shield className="w-4 h-4 text-slate-700" />
            <span>VaultRun — self-hosted secure AI agent runtime</span>
          </div>
          <div className="flex items-center gap-4">
            <a
              href="https://github.com/nickvd7/vaultrun"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-slate-400 transition-colors"
            >
              GitHub
            </a>
            <a
              href="https://github.com/nickvd7/vaultrun/blob/main/CHANGELOG.md"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-slate-400 transition-colors"
            >
              Changelog
            </a>
            <span className="font-mono">v0.2.1</span>
          </div>
        </div>
      </footer>
    </div>
  );
}
