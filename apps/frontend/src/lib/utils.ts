import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Shared shape for every badge/chip so they look the same on all pages. */
export const badgeBase = "inline-flex items-center px-2 py-0.5 rounded text-xs font-mono";

export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const k = 1024;
  const sizes = ["B", "KB", "MB", "GB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`;
}

const dateTimeFormat = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });
const dateTimeZoneFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
  timeZoneName: "short",
});

/**
 * The one absolute date format used across the dashboard, in the viewer's time
 * zone ("Oct 16, 2026, 8:44 AM"). Lists use relativeTime() with this as the
 * tooltip; there is no third notation.
 */
export function formatDate(iso: string): string {
  return dateTimeFormat.format(new Date(iso));
}

/** formatDate plus the time zone, for moments that matter (key expiry). */
export function formatDateWithZone(iso: string): string {
  return dateTimeZoneFormat.format(new Date(iso));
}

/** Value for <input type="datetime-local">: local wall-clock time, not UTC. */
export function toDateTimeLocalValue(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "5m ago" for recent times, the absolute format beyond a week. */
export function relativeTime(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  if (diff < 60_000) return "just now";
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
  if (diff < 7 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`;
  return formatDate(iso);
}

export function statusColor(status: string): string {
  switch (status) {
    case "running":
    case "completed":
      return "text-green-400";
    case "created":
      return "text-blue-400";
    case "failed":
    case "error":
      return "text-red-400";
    case "timeout":
      return "text-yellow-400";
    case "stopped":
      return "text-gray-400";
    default:
      return "text-gray-300";
  }
}

export function statusBadgeClass(status: string): string {
  switch (status) {
    case "running":
      return "bg-green-900/40 text-green-400 border border-green-800";
    case "completed":
      return "bg-emerald-900/40 text-emerald-400 border border-emerald-800";
    case "created":
      return "bg-blue-900/40 text-blue-400 border border-blue-800";
    case "failed":
    case "error":
      return "bg-red-900/40 text-red-400 border border-red-800";
    case "timeout":
      return "bg-yellow-900/40 text-yellow-400 border border-yellow-800";
    case "stopped":
      return "bg-gray-900/40 text-gray-400 border border-gray-700";
    default:
      return "bg-gray-800 text-gray-300 border border-gray-700";
  }
}
