import { cn, statusBadgeClass, badgeBase } from "@/lib/utils";

interface Props {
  status: string;
  className?: string;
}

export function StatusBadge({ status, className }: Props) {
  return (
    <span
      className={cn(
        badgeBase,
        "uppercase tracking-wide",
        statusBadgeClass(status),
        className
      )}
    >
      {status}
    </span>
  );
}
