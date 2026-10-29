import { Info, AlertCircle } from "lucide-react";

export function Notice({
  tone = "info",
  title,
  children,
}: {
  tone?: "info" | "error";
  title?: string;
  children: React.ReactNode;
}) {
  const styles =
    tone === "error"
      ? "border-red-900/60 bg-red-950/30 text-red-200"
      : "border-indigo-900/60 bg-indigo-950/30 text-indigo-100";
  const Icon = tone === "error" ? AlertCircle : Info;
  return (
    <div role={tone === "error" ? "alert" : "note"} className={`flex items-start gap-3 rounded-lg border px-4 py-3 text-sm ${styles}`}>
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="space-y-1">
        {title && <p className="font-medium">{title}</p>}
        <div className="text-slate-300">{children}</div>
      </div>
    </div>
  );
}

export function MasterKeyNotice({ what }: { what: string }) {
  return (
    <Notice title="Master key required">
      {what} is only available with the master key (<code className="font-mono">MASTER_API_KEY</code> in your{" "}
      <code className="font-mono">.env</code>). You are signed in without it; sign in with the master key to use this page.
    </Notice>
  );
}
