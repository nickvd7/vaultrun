import Link from "next/link";
import { ArrowLeft } from "lucide-react";

export default function AppNotFound() {
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
