"use client";

import { useState, useEffect } from "react";
import { usePathname } from "next/navigation";
import { Menu, Shield } from "lucide-react";
import { Sidebar } from "@/components/Sidebar";
import { cn } from "@/lib/utils";

export function AppShell({ children }: { children: React.ReactNode }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const pathname = usePathname();

  useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  useEffect(() => {
    if (!mobileOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMobileOpen(false);
    };
    document.addEventListener("keydown", onKey);
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
    };
  }, [mobileOpen]);

  return (
    <div className="flex h-screen overflow-hidden">
      {mobileOpen && (
        <button
          type="button"
          className="fixed inset-0 z-40 bg-black/60 lg:hidden"
          aria-label="Close menu"
          onClick={() => setMobileOpen(false)}
        />
      )}

      <Sidebar mobileOpen={mobileOpen} onNavigate={() => setMobileOpen(false)} />

      <div className="flex flex-1 flex-col min-w-0 overflow-hidden">
        <header className="flex lg:hidden items-center gap-3 px-4 py-3 border-b border-slate-800 bg-[#0d0d14] shrink-0">
          <button
            type="button"
            className="p-2 rounded-md text-slate-400 hover:text-slate-200 hover:bg-slate-800/50"
            aria-expanded={mobileOpen}
            aria-label={mobileOpen ? "Close menu" : "Open menu"}
            onClick={() => setMobileOpen((o) => !o)}
          >
            <Menu className="w-5 h-5" />
          </button>
          <div className="flex items-center gap-2">
            <Shield className="w-5 h-5 text-indigo-400" />
            <span className="font-semibold text-slate-100 tracking-tight">VaultRun</span>
          </div>
        </header>

        <main className={cn("flex-1 overflow-auto p-4 lg:p-6")}>{children}</main>
      </div>
    </div>
  );
}
