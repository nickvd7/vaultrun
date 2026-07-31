"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { api } from "@/lib/api";
import { useApiKey } from "@/components/ApiKeyGate";

export type OrgOption = {
  id: string;
  name: string;
  slug: string;
  role: string;
};

type OrgContextValue = {
  orgs: OrgOption[];
  orgId: string | null;
  setOrgId: (id: string | null) => void;
  loading: boolean;
  refresh: () => void;
};

const OrgContext = createContext<OrgContextValue | null>(null);
const STORAGE_KEY = "vaultrun_org_id";

export function OrgProvider({ children }: { children: React.ReactNode }) {
  const { apiKey } = useApiKey();
  const [orgs, setOrgs] = useState<OrgOption[]>([]);
  const [orgId, setOrgIdState] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const refresh = useCallback(() => {
    if (!apiKey) {
      setOrgs([]);
      setOrgIdState(null);
      return;
    }
    setLoading(true);
    api.orgs
      .mine()
      .then(({ orgs: list }) => {
        setOrgs(list);
        const stored = typeof window !== "undefined" ? localStorage.getItem(STORAGE_KEY) : null;
        if (stored && list.some((o) => o.id === stored)) {
          setOrgIdState(stored);
        } else if (stored && !list.some((o) => o.id === stored)) {
          localStorage.removeItem(STORAGE_KEY);
          setOrgIdState(null);
        }
      })
      .catch(() => setOrgs([]))
      .finally(() => setLoading(false));
  }, [apiKey]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  const setOrgId = useCallback((id: string | null) => {
    setOrgIdState(id);
    if (typeof window === "undefined") return;
    if (id) localStorage.setItem(STORAGE_KEY, id);
    else localStorage.removeItem(STORAGE_KEY);
  }, []);

  const value = useMemo(
    () => ({ orgs, orgId, setOrgId, loading, refresh }),
    [orgs, orgId, setOrgId, loading, refresh]
  );

  return <OrgContext.Provider value={value}>{children}</OrgContext.Provider>;
}

export function useOrg() {
  const ctx = useContext(OrgContext);
  if (!ctx) throw new Error("useOrg must be used within OrgProvider");
  return ctx;
}
