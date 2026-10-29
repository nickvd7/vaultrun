"use client";

import { useEffect, useState } from "react";
import { api, type ServerInfo } from "@/lib/api";

let cached: Promise<ServerInfo | null> | null = null;

/** What the API offers (edition, sandbox runtime, evidence). Fetched once per page load; null until known or on older APIs. */
export function useServerInfo(): ServerInfo | null {
  const [info, setInfo] = useState<ServerInfo | null>(null);
  useEffect(() => {
    let live = true;
    if (!cached) cached = api.info().catch(() => null);
    cached.then((i) => {
      if (live) setInfo(i);
    });
    return () => {
      live = false;
    };
  }, []);
  return info;
}

/** Human name of the sandbox isolation, e.g. "gVisor (runsc)". */
export function isolationLabel(info: ServerInfo): string {
  if (info.sandbox.gvisor) return `gVisor (${info.sandbox.runtime})`;
  return info.sandbox.runtime ? info.sandbox.runtime : "Docker default (runc)";
}
