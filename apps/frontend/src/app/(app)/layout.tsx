import { AppShell } from "@/components/AppShell";
import { ApiKeyGate } from "@/components/ApiKeyGate";
import { OrgProvider } from "@/components/OrgContext";
import { ToastProvider } from "@/components/ui/Toast";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <ApiKeyGate>
      <OrgProvider>
        <ToastProvider>
          <AppShell>{children}</AppShell>
        </ToastProvider>
      </OrgProvider>
    </ApiKeyGate>
  );
}
