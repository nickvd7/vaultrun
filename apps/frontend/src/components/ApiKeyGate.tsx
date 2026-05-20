"use client";

import { createContext, useContext, useEffect, useState, useCallback } from "react";
import { Shield, Eye, EyeOff } from "lucide-react";

interface ApiKeyContextValue {
  apiKey: string;
  setApiKey: (key: string) => void;
  clearApiKey: () => void;
}

const ApiKeyContext = createContext<ApiKeyContextValue>({
  apiKey: "",
  setApiKey: () => {},
  clearApiKey: () => {},
});

export function useApiKey() {
  return useContext(ApiKeyContext);
}

const STORAGE_KEY = "vaultrun_api_key";

export function ApiKeyGate({ children }: { children: React.ReactNode }) {
  const [apiKey, setApiKeyState] = useState<string | null>(null); // null = not yet loaded
  const [input, setInput] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [error, setError] = useState("");
  const [testing, setTesting] = useState(false);

  // Load from localStorage on mount (client-only)
  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEY) || "";
    setApiKeyState(stored);
  }, []);

  const setApiKey = useCallback((key: string) => {
    localStorage.setItem(STORAGE_KEY, key);
    setApiKeyState(key);
  }, []);

  const clearApiKey = useCallback(() => {
    localStorage.removeItem(STORAGE_KEY);
    setApiKeyState("");
  }, []);

  const handleConnect = async () => {
    const key = input.trim();
    if (!key) {
      setError("API key is required");
      return;
    }
    setTesting(true);
    setError("");
    try {
      const resp = await fetch("/api/v1/sessions?limit=1", {
        headers: { "X-API-Key": key },
      });
      if (resp.status === 401) {
        setError("Invalid API key — check your key and try again");
        return;
      }
      if (!resp.ok && resp.status !== 200) {
        setError(`Server returned ${resp.status} — is the API reachable?`);
        return;
      }
      setApiKey(key);
      setInput("");
    } catch {
      setError("Cannot reach the API — check NEXT_PUBLIC_API_URL");
    } finally {
      setTesting(false);
    }
  };

  // Still reading from localStorage
  if (apiKey === null) return null;

  // No key set — show connect screen
  if (!apiKey) {
    return (
      <div className="flex h-screen items-center justify-center bg-[#0a0a0f]">
        <div className="w-full max-w-sm space-y-6 px-6">
          <div className="flex flex-col items-center gap-3">
            <div className="flex items-center justify-center w-12 h-12 rounded-xl bg-indigo-900/40 border border-indigo-700/40">
              <Shield className="w-6 h-6 text-indigo-400" />
            </div>
            <div className="text-center">
              <h1 className="text-xl font-semibold text-slate-100">Connect to VaultRun</h1>
              <p className="text-slate-500 text-sm mt-1">Enter your API key to continue</p>
            </div>
          </div>

          <div className="space-y-3">
            <div className="relative">
              <input
                type={showKey ? "text" : "password"}
                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-3 pr-10 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-600"
                placeholder="vr_…"
                value={input}
                onChange={(e) => { setInput(e.target.value); setError(""); }}
                onKeyDown={(e) => e.key === "Enter" && handleConnect()}
                autoFocus
              />
              <button
                type="button"
                onClick={() => setShowKey(!showKey)}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300"
              >
                {showKey ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
              </button>
            </div>

            {error && (
              <p className="text-red-400 text-xs">{error}</p>
            )}

            <button
              onClick={handleConnect}
              disabled={testing || !input.trim()}
              className="w-full py-3 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-500 disabled:opacity-50 transition-colors"
            >
              {testing ? "Verifying…" : "Connect"}
            </button>
          </div>

          <p className="text-center text-xs text-slate-600">
            Generate a key with{" "}
            <code className="font-mono text-slate-500">make bootstrap-key</code>
          </p>
        </div>
      </div>
    );
  }

  return (
    <ApiKeyContext.Provider value={{ apiKey, setApiKey, clearApiKey }}>
      {children}
    </ApiKeyContext.Provider>
  );
}
