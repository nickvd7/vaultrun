import { cn, statusBadgeClass } from "@/lib/utils";

interface Props {
  status: string;
  className?: string;
}

export function StatusBadge({ status, className }: Props) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-2 py-0.5 rounded text-xs font-mono uppercase tracking-wide",
        statusBadgeClass(status),
        className
      )}
    >
      {status}
    </span>
  );
}
