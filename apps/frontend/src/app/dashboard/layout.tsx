"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Shield,
  LayoutDashboard,
  ScrollText,
  Home,
  Box,
} from "lucide-react";

const navItems = [
  { href: "/dashboard", label: "Sessions", icon: Box },
  { href: "/dashboard/audit", label: "Audit Logs", icon: ScrollText },
];

export default function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const pathname = usePathname();

  return (
    <div className="min-h-screen bg-[#0a0a0f] text-slate-200 flex">
      {/* Sidebar */}
      <aside className="w-56 shrink-0 border-r border-slate-800 flex flex-col">
        {/* Logo */}
        <div className="px-5 py-4 border-b border-slate-800 flex items-center gap-2">
          <Shield className="w-4 h-4 text-indigo-400" />
          <span className="font-semibold text-slate-100 tracking-tight text-sm">VaultRun</span>
        </div>

        {/* Nav */}
        <nav className="flex-1 px-3 py-4 space-y-1">
          {navItems.map(({ href, label, icon: Icon }) => {
            const active =
              href === "/dashboard"
                ? pathname === "/dashboard" || pathname.startsWith("/dashboard/sessions")
                : pathname.startsWith(href);
            return (
              <Link
                key={href}
                href={href}
                className={`flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm transition-colors ${
                  active
                    ? "bg-indigo-900/40 text-indigo-300 border border-indigo-700/30"
                    : "text-slate-400 hover:text-slate-200 hover:bg-slate-800/40"
                }`}
              >
                <Icon className="w-4 h-4" />
                {label}
              </Link>
            );
          })}
        </nav>

        {/* Footer */}
        <div className="px-5 py-4 border-t border-slate-800">
          <Link
            href="/"
            className="flex items-center gap-2 text-xs text-slate-600 hover:text-slate-400 transition-colors"
          >
            <Home className="w-3.5 h-3.5" />
            Back to home
          </Link>
        </div>
      </aside>

      {/* Main */}
      <main className="flex-1 min-w-0 overflow-auto">
        {children}
      </main>
    </div>
  );
}
