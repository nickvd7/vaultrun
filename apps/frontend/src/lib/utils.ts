import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

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

export function formatDate(iso: string): string {
  return new Date(iso).toLocaleString();
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
