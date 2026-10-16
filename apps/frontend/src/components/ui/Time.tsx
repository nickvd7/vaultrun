import { formatDate, relativeTime } from "@/lib/utils";

/**
 * A timestamp as "5m ago" with the exact local time on hover, or the exact time
 * itself with `absolute`. Every date in the dashboard goes through here or
 * formatDate so the notation is the same on every page.
 */
export function Time({ value, absolute = false }: { value: string; absolute?: boolean }) {
  const exact = formatDate(value);
  return (
    <time dateTime={value} title={absolute ? new Date(value).toISOString() : exact}>
      {absolute ? exact : relativeTime(value)}
    </time>
  );
}
