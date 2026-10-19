"use client";

import { Fragment } from "react";
import { ShieldCheck, ShieldAlert, ShieldQuestion, ArrowUpDown } from "lucide-react";
import type { AuditLog } from "@/types";

export type Namespace = "session" | "file" | "command" | "apikey" | "secret" | "other";

export function namespace(action: string): Namespace {
  if (action.startsWith("session.")) return "session";
  if (action.startsWith("file.")) return "file";
  if (action.startsWith("command.")) return "command";
  if (action.startsWith("apikey.")) return "apikey";
  if (action.startsWith("secret.")) return "secret";
  return "other";
}

export const NS_CHIP: Record<Namespace, string> = {
  session: "bg-blue-900/30 text-blue-200 border border-blue-800/50",
  file: "bg-emerald-900/30 text-emerald-200 border border-emerald-800/50",
  command: "bg-indigo-900/30 text-indigo-200 border border-indigo-800/50",
  apikey: "bg-amber-900/30 text-amber-200 border border-amber-800/50",
  secret: "bg-rose-900/30 text-rose-200 border border-rose-800/50",
  other: "bg-slate-800 text-slate-300 border border-slate-700",
};

export function actorLabel(l: AuditLog): string {
  const name = l.actor_name || l.actor;
  return name.length > 20 ? name.slice(0, 20) + "…" : name;
}

export function MetadataPanel({ meta }: { meta: Record<string, unknown> }) {
  const entries = Object.entries(meta);
  if (entries.length === 0) return <span className="text-slate-300">—</span>;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
      {entries.map(([k, v]) => (
        <Fragment key={k}>
          <dt className="text-slate-300 whitespace-nowrap">{k}</dt>
          <dd className="text-slate-200 font-mono break-all" title={displayValue(v).title}>
            {displayValue(v).text}
          </dd>
        </Fragment>
      ))}
    </dl>
  );
}

export type SigKind = "valid" | "invalid" | "unsigned" | "unverifiable" | "unverified";

interface SigState {
  kind: SigKind;
  label: string;
  title: string;
  className: string;
}

export const SIG_STATES: Record<SigKind, Omit<SigState, "kind">> = {
  valid: { label: "Verified", title: "Signature verified", className: "text-emerald-300" },
  invalid: {
    label: "Invalid",
    title: "Signature does not match, or was removed: this entry may have been altered",
    className: "text-red-300",
  },
  unsigned: {
    label: "Unsigned",
    title: "Written before audit signing was enabled",
    className: "text-slate-300",
  },
  unverifiable: {
    label: "Legacy",
    title: "Signed by an older version whose signatures cannot be verified",
    className: "text-slate-300",
  },
  unverified: {
    label: "Unverified",
    title: "Signature does not verify (this API does not say whether the entry predates signing)",
    className: "text-amber-300",
  },
};

/** What to show for an entry's signature; null when the server does not sign entries. */
export function sigState(l: AuditLog): SigState | null {
  const kind: SigKind | null = l.sig_status ?? (l.sig_valid === true ? "valid" : l.sig_valid === false ? "unverified" : null);
  return kind ? { kind, ...SIG_STATES[kind] } : null;
}

export function SigCell({ log }: { log: AuditLog }) {
  const st = sigState(log);
  if (!st) return null;
  const affected = st.kind === "invalid" && log.sig_kind === "chain";
  const label = affected ? "Affected" : st.kind === "invalid" && log.sig_kind === "altered" ? "Altered" : st.label;
  const color = affected ? "text-amber-300" : st.className;
  const title = affected
    ? "This entry is intact, but the entry before it was changed or deleted"
    : log.sig_reason
      ? `${st.title} (${log.sig_reason})`
      : st.title;
  const Icon = st.kind === "valid" ? ShieldCheck : st.kind === "invalid" || st.kind === "unverified" ? ShieldAlert : ShieldQuestion;
  return (
    <span title={log.sig_detail || title} className={`inline-flex flex-col text-xs ${color}`}>
      <span className="inline-flex items-center gap-1">
        <Icon className="w-3.5 h-3.5" aria-hidden="true" />
        {label}
      </span>
      {st.kind === "invalid" && log.sig_reason && <span className="font-normal">{log.sig_reason}</span>}
    </span>
  );
}

export function OutOfOrderChip({ prevSeq }: { prevSeq?: number }) {
  return (
    <span
      className="mt-1 inline-flex items-center gap-1 text-xs text-amber-300"
      title={`Dated earlier than the entry before it${prevSeq ? ` (#${prevSeq})` : ""}: an untouched log never shows this`}
    >
      <ArrowUpDown className="w-3 h-3" aria-hidden="true" />
      Out of order
    </span>
  );
}


const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Shorten a UUID for a table cell. The full value stays available as a tooltip. */
function displayValue(v: unknown): { text: string; title?: string } {
  if (typeof v === "string") {
    if (UUID_RE.test(v)) return { text: `${v.slice(0, 8)}…`, title: v };
    return { text: v };
  }
  if (Array.isArray(v) && v.every((x) => typeof x === "string" || typeof x === "number")) {
    return { text: v.join(" ") };
  }
  return { text: JSON.stringify(v) };
}

/** Compact, wrapping key/value list for table cells (the full panel is for expanded rows). */
export function MetadataInline({ meta }: { meta: Record<string, unknown> }) {
  const entries = Object.entries(meta);
  if (entries.length === 0) return <span className="text-slate-300">—</span>;
  return (
    <span className="flex flex-wrap gap-x-3 gap-y-0.5">
      {entries.map(([k, v]) => {
        const shown = displayValue(v);
        return (
          <span key={k} className="whitespace-nowrap">
            <span className="text-slate-300">{k}</span>{" "}
            <span className="text-slate-100 font-mono" title={shown.title}>{shown.text}</span>
          </span>
        );
      })}
    </span>
  );
}
