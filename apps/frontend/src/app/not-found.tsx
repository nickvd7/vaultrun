import Link from "next/link";
import { Shield, ArrowLeft } from "lucide-react";

export default function NotFound() {
  return (
    <div className="flex min-h-screen flex-col bg-[#0a0a0f]">
      <header className="border-b border-slate-800 bg-[#0d0d14]">
        <nav aria-label="Main" className="mx-auto flex max-w-6xl items-center justify-between px-6 py-4">
          <Link href="/" className="flex items-center gap-2 font-semibold tracking-tight text-slate-100">
            <Shield className="h-5 w-5 text-indigo-400" aria-hidden="true" />
            VaultRun
          </Link>
          <div className="flex items-center gap-4 text-sm">
            <Link href="/dashboard" className="text-slate-300 hover:text-white">Dashboard</Link>
            <Link href="/sessions" className="text-slate-300 hover:text-white">Sessions</Link>
            <Link href="/audit" className="text-slate-300 hover:text-white">Audit Logs</Link>
          </div>
        </nav>
      </header>
      <main className="flex flex-1 items-center justify-center px-6">
        <div className="max-w-md space-y-4 text-center">
          <p className="font-mono text-sm text-indigo-300">404</p>
          <h1 className="text-2xl font-semibold text-slate-100">Page not found</h1>
          <p className="text-sm text-slate-400">The page you are looking for does not exist or has moved.</p>
          <Link
            href="/dashboard"
            className="inline-flex items-center gap-1.5 rounded-lg bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500"
          >
            <ArrowLeft className="h-4 w-4" aria-hidden="true" /> Back to the dashboard
          </Link>
        </div>
      </main>
    </div>
  );
}
