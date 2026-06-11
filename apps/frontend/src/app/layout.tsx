import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "VaultRun — Secure AI Agent Runtime",
  description: "Self-hosted secure sandbox runtime for AI agents",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body className="bg-[#0a0a0f] text-slate-200">{children}</body>
    </html>
  );
}
