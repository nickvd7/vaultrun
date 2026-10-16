"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Shield, ArrowLeft } from "lucide-react";
import { AppShell } from "@/components/AppShell";
import { ApiKeyGate } from "@/components/ApiKeyGate";
import { OrgProvider } from "@/components/OrgContext";
import { ToastProvider } from "@/components/ui/Toast";

function Message() {
  return (
    <div className="flex min-h-[60vh] items-center justify-center">
      <div className="max-w-md space-y-4 text-center">
        <p className="font-mono text-sm text-indigo-300">404</p>
        <h1 className="text-2xl font-semibold text-slate-100">Page not found</h1>
        <p className="text-sm text-slate-300">The page you are looking for does not exist or has moved.</p>
        <Link
          href="/dashboard"
          className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500"
        >
          <ArrowLeft className="h-4 w-4" aria-hidden="true" /> Back to the dashboard
        </Link>
      </div>
    </div>
  );
}

/**
 * The 404 page. Someone who is signed in gets it inside the app (sidebar and
 * navigation, like every other page); anyone else gets a plain page with links
 * to the public site. It is the root not-found page, so the response keeps its
 * real 404 status.
 */
export function NotFoundView() {
  const [signedIn, setSignedIn] = useState<boolean | null>(null);

  useEffect(() => {
    setSignedIn(Boolean(localStorage.getItem("vaultrun_api_key")));
  }, []);

  if (signedIn === null) return null;

  if (signedIn) {
    return (
      <ApiKeyGate>
        <OrgProvider>
          <ToastProvider>
            <AppShell>
              <Message />
            </AppShell>
          </ToastProvider>
        </OrgProvider>
      </ApiKeyGate>
    );
  }

  return (
    <div className="flex min-h-screen flex-col bg-[#0a0a0f]">
      <header className="border-b border-slate-800 bg-[#0d0d14]">
        <nav aria-label="Main" className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
          <Link href="/" className="flex items-center gap-2 font-semibold tracking-tight text-slate-100">
            <Shield className="h-5 w-5 text-indigo-400" aria-hidden="true" />
            VaultRun
          </Link>
          <Link href="/dashboard" className="text-sm text-slate-300 hover:text-white">
            Dashboard
          </Link>
        </nav>
      </header>
      <main className="flex flex-1 items-center justify-center px-6">
        <Message />
      </main>
    </div>
  );
}
