"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Shield, LayoutDashboard, Terminal, FileText, ScrollText, KeyRound, LogOut, ShieldCheck, Archive, Package } from "lucide-react";
import { cn } from "@/lib/utils";
import { useApiKey } from "@/components/ApiKeyGate";

const nav = [
  { href: "/dashboard", label: "Dashboard", icon: LayoutDashboard },
  { href: "/sessions", label: "Sessions", icon: Terminal },
  { href: "/snapshots", label: "Snapshots", icon: Archive },
  { href: "/artifacts", label: "Artifacts", icon: Package },
  { href: "/keys", label: "API Keys", icon: KeyRound },
  { href: "/audit", label: "Audit Logs", icon: ScrollText },
  { href: "/policy", label: "Policy", icon: ShieldCheck },
];

export function Sidebar() {
  const pathname = usePathname();
  const { apiKey, clearApiKey } = useApiKey();

  return (
    <aside className="w-56 shrink-0 flex flex-col bg-[#0d0d14] border-r border-slate-800">
      {/* Logo */}
      <div className="flex items-center gap-2 px-4 py-5 border-b border-slate-800">
        <Shield className="w-5 h-5 text-indigo-400" />
        <span className="font-semibold text-slate-100 tracking-tight">VaultRun</span>
      </div>

      {/* Nav */}
      <nav className="flex-1 px-2 py-3 space-y-0.5">
        {nav.map(({ href, label, icon: Icon }) => (
          <Link
            key={href}
            href={href}
            className={cn(
              "flex items-center gap-3 px-3 py-2 rounded-md text-sm transition-colors",
              pathname === href || (href !== "/" && pathname.startsWith(href))
                ? "bg-indigo-900/40 text-indigo-300"
                : "text-slate-400 hover:bg-slate-800/50 hover:text-slate-200"
            )}
          >
            <Icon className="w-4 h-4 shrink-0" />
            {label}
          </Link>
        ))}
      </nav>

      {/* Footer */}
      <div className="px-2 py-3 border-t border-slate-800 space-y-1">
        {/* Connected key indicator */}
        {apiKey && (
          <div className="px-3 py-1.5 text-xs text-slate-600 font-mono truncate">
            {apiKey.slice(0, 12)}…
          </div>
        )}
        <button
          onClick={clearApiKey}
          className="w-full flex items-center gap-2 px-3 py-2 rounded-md text-xs text-slate-500 hover:bg-slate-800/50 hover:text-slate-300 transition-colors"
        >
          <LogOut className="w-3.5 h-3.5" />
          Disconnect
        </button>
        <div className="flex items-center gap-1.5 px-3 py-1 text-xs text-slate-700">
          <FileText className="w-3 h-3" />
          <span>v0.2.1</span>
        </div>
      </div>
    </aside>
  );
}
