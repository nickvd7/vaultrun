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
} from "lucide-react";

const features = [
  {
    icon: Box,
    title: "Isolated Docker Sandboxes",
    desc: "Every session runs in its own container with dropped capabilities, seccomp filtering, and no access to the host.",
  },
  {
    icon: Shield,
    title: "Zero-Trust Security",
    desc: "API key authentication, path-traversal prevention, non-root execution, and optional network isolation by default.",
  },
  {
    icon: Globe,
    title: "MCP-Native",
    desc: "Built-in Model Context Protocol server exposes all 16 tools over stdio or HTTP — works with Claude, OpenAI, OpenRouter, and any MCP client.",
  },
  {
    icon: Lock,
    title: "Fully Self-Hosted",
    desc: "Your code, your data, your infrastructure. No telemetry, no SaaS dependency, no data leaving your network.",
  },
  {
    icon: ScrollText,
    title: "Immutable Audit Trail",
    desc: "Every action is logged with HMAC signatures. Searchable, tamper-evident, and exportable to your SIEM.",
  },
  {
    icon: Zap,
    title: "Fast & Scalable",
    desc: "Warm container pool, async job queues via Redis, and Prometheus metrics — ready for production workloads.",
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

export default function LandingPage() {
  return (
    <div className="min-h-screen bg-[#0a0a0f] text-slate-200 flex flex-col">
      {/* Nav */}
      <header className="border-b border-slate-800/60 sticky top-0 z-10 bg-[#0a0a0f]/90 backdrop-blur">
        <div className="max-w-6xl mx-auto px-6 py-4 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Shield className="w-5 h-5 text-indigo-400" />
            <span className="font-semibold text-slate-100 tracking-tight">VaultRun</span>
          </div>
          <div className="flex items-center gap-3">
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
      <section className="flex-1 max-w-6xl mx-auto px-6 pt-24 pb-20 text-center">
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
          manage files, and run commands — safely, on your own infrastructure.
        </p>
        <div className="flex items-center justify-center gap-4">
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
      </section>

      {/* Features */}
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
                  <Icon className="w-4.5 h-4.5 text-indigo-400" />
                </div>
                <h3 className="font-medium text-slate-100 text-sm">{title}</h3>
              </div>
              <p className="text-slate-500 text-sm leading-relaxed">{desc}</p>
            </div>
          ))}
        </div>
      </section>

      {/* Quick start */}
      <section className="max-w-6xl mx-auto px-6 pb-24">
        <div className="bg-[#0f0f1a] border border-slate-800 rounded-2xl p-8 md:p-12">
          <div className="grid md:grid-cols-2 gap-10 items-center">
            <div>
              <div className="flex items-center gap-2 mb-4">
                <Cpu className="w-5 h-5 text-indigo-400" />
                <span className="text-xs uppercase tracking-widest text-indigo-400 font-medium">MCP integration</span>
              </div>
              <h2 className="text-2xl font-semibold text-slate-100 mb-4">
                Works with any MCP-compatible AI
              </h2>
              <p className="text-slate-400 text-sm leading-relaxed mb-6">
                Add VaultRun to Claude Desktop, Claude Code, or any platform that supports the
                Model Context Protocol. Your AI gets 16 tools to create sandboxes, run code,
                manage files, and inspect audit logs — all controlled by you.
              </p>
              <Link
                href="/dashboard"
                className="inline-flex items-center gap-2 px-5 py-2.5 rounded-lg bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-medium transition-colors"
              >
                Open Dashboard <ArrowRight className="w-3.5 h-3.5" />
              </Link>
            </div>
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
          </div>
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-slate-800/60 py-8">
        <div className="max-w-6xl mx-auto px-6 flex items-center justify-between text-xs text-slate-600">
          <div className="flex items-center gap-2">
            <Shield className="w-4 h-4 text-slate-700" />
            <span>VaultRun — self-hosted secure AI agent runtime</span>
          </div>
          <span>v0.1.0</span>
        </div>
      </footer>
    </div>
  );
}
