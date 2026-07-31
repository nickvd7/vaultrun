import { AppShell } from "@/components/AppShell";
import { ApiKeyGate } from "@/components/ApiKeyGate";
import { OrgProvider } from "@/components/OrgContext";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <ApiKeyGate>
      <OrgProvider>
        <AppShell>{children}</AppShell>
      </OrgProvider>
    </ApiKeyGate>
  );
}
