import { AppShell } from "@/components/AppShell";
import { ApiKeyGate } from "@/components/ApiKeyGate";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <ApiKeyGate>
      <AppShell>{children}</AppShell>
    </ApiKeyGate>
  );
}
