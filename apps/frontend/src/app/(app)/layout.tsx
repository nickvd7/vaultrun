import { Sidebar } from "@/components/Sidebar";
import { ApiKeyGate } from "@/components/ApiKeyGate";

export default function AppLayout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex h-screen overflow-hidden">
      <ApiKeyGate>
        <Sidebar />
        <main className="flex-1 overflow-auto p-6">{children}</main>
      </ApiKeyGate>
    </div>
  );
}
