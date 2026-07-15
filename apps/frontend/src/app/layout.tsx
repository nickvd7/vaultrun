import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: {
    default: "VaultRun — Secure AI Agent Runtime",
    template: "%s · VaultRun",
  },
  description:
    "Self-hosted secure sandbox runtime for AI agents. Isolated Docker sessions, MCP tools, and an HMAC-signed audit trail.",
  openGraph: {
    title: "VaultRun — Secure AI Agent Runtime",
    description:
      "Self-hosted secure sandbox runtime for AI agents. Isolated Docker sessions and a 53-tool MCP server.",
    siteName: "VaultRun",
    type: "website",
  },
  twitter: {
    card: "summary",
    title: "VaultRun — Secure AI Agent Runtime",
    description:
      "Self-hosted secure sandbox runtime for AI agents. Isolated Docker sessions and a 53-tool MCP server.",
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="bg-[#0a0a0f] text-slate-200">{children}</body>
    </html>
  );
}
