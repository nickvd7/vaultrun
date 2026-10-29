"use client";

import { createContext, useContext, useEffect, useState, useCallback } from "react";
import { Shield, Eye, EyeOff, LogIn } from "lucide-react";
import { UNAUTHORIZED_EVENT, SSO_SESSION, authHeaders, sso } from "@/lib/api";

interface ApiKeyContextValue {
  apiKey: string;
  setApiKey: (key: string) => void;
  clearApiKey: () => void;
  /** True when signed in with single sign-on rather than an API key. */
  isSSO: boolean;
  /** true = master key, false = regular key, null = still checking / unknown. */
  isMaster: boolean | null;
  /** Name of the signed-in key ("master" for the master key); null while unknown. */
  keyName: string | null;
  /** false until the role check finished (successfully or not); pages that need the role wait for it. */
  roleReady: boolean;
}

const ApiKeyContext = createContext<ApiKeyContextValue>({
  apiKey: "",
  setApiKey: () => {},
  clearApiKey: () => {},
  isSSO: false,
  isMaster: null,
  keyName: null,
  roleReady: false,
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
  const [isMaster, setIsMaster] = useState<boolean | null>(null);
  const [keyName, setKeyName] = useState<string | null>(null);
  const [roleReady, setRoleReady] = useState(false);
  const [notice, setNotice] = useState("");
  const [providers, setProviders] = useState<{ oidc: boolean; saml: boolean } | null>(null);

  const rejectKey = useCallback(() => {
    const wasSSO = localStorage.getItem(STORAGE_KEY) === SSO_SESSION;
    localStorage.removeItem(STORAGE_KEY);
    setNotice(
      wasSSO
        ? "Your single sign-on session ended (expired, signed out or revoked). Sign in again."
        : "Your saved API key was rejected (expired or revoked). Enter a valid key to sign in again."
    );
    setApiKeyState("");
  }, []);

  useEffect(() => {
    window.addEventListener(UNAUTHORIZED_EVENT, rejectKey);
    return () => window.removeEventListener(UNAUTHORIZED_EVENT, rejectKey);
  }, [rejectKey]);

  // Load from localStorage on mount (client-only). Without a stored key, ask
  // whether the server offers single sign-on and whether the browser already
  // holds a session (it does right after the IdP redirected back).
  useEffect(() => {
    const stored = localStorage.getItem(STORAGE_KEY) || "";
    let cancelled = false;
    (async () => {
      const offered = await sso.providers();
      if (cancelled) return;
      setProviders(offered);
      if (!stored && offered && (await sso.hasSession())) {
        if (cancelled) return;
        localStorage.setItem(STORAGE_KEY, SSO_SESSION);
        setApiKeyState(SSO_SESSION);
        return;
      }
      if (!cancelled && stored) return;
      if (!cancelled) setApiKeyState("");
    })();
    if (stored) setApiKeyState(stored);
    return () => {
      cancelled = true;
    };
  }, []);

  // Ask the API what this key may do. Older APIs have no /whoami; they are
  // probed through the master-only key list instead (which logs a 403 for
  // ordinary keys in the browser console, the reason /whoami exists).
  useEffect(() => {
    setRoleReady(false);
    if (!apiKey) {
      setIsMaster(null);
      setKeyName(null);
      return;
    }
    let cancelled = false;
    const headers = authHeaders(apiKey);
    (async () => {
      try {
        const who = await fetch("/api/v1/whoami", { headers });
        if (cancelled) return;
        if (who.ok) {
          const body = (await who.json()) as { master?: boolean; actor?: string };
          if (cancelled) return;
          setIsMaster(Boolean(body.master));
          setKeyName(body.actor ?? null);
          return;
        }
        if (who.status === 401) {
          rejectKey();
          return;
        }
        if (who.status !== 404) {
          setIsMaster(null);
          return;
        }
        const probe = await fetch("/api/v1/keys?limit=1", { headers });
        if (cancelled) return;
        setIsMaster(probe.ok ? true : probe.status === 403 ? false : null);
        setKeyName(null);
      } catch {
        if (!cancelled) setIsMaster(null);
      } finally {
        if (!cancelled) setRoleReady(true);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [apiKey, rejectKey]);

  const setApiKey = useCallback((key: string) => {
    localStorage.setItem(STORAGE_KEY, key);
    setNotice("");
    setApiKeyState(key);
  }, []);

  const clearApiKey = useCallback(() => {
    const wasSSO = localStorage.getItem(STORAGE_KEY) === SSO_SESSION;
    localStorage.removeItem(STORAGE_KEY);
    setNotice("");
    setApiKeyState("");
    if (wasSSO) {
      sso.logout().catch(() =>
        setNotice(
          "Signed out in this browser, but the server could not revoke the session. " +
            "Anyone holding a copy of the session cookie can use it until it expires; tell your administrator."
        )
      );
    }
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
      setError("Cannot reach the API — check that the api service is running (make logs)");
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
              <p className="text-slate-300 text-sm mt-1">
                {providers ? "Sign in with your organisation's account" : "Enter your API key to continue"}
              </p>
            </div>
          </div>

          {providers && (
            <div className="space-y-2">
              {providers.oidc && (
                <a
                  href={sso.loginURL("oidc")}
                  className="w-full flex items-center justify-center gap-2 py-3 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-500 transition-colors"
                >
                  <LogIn className="w-4 h-4" aria-hidden="true" /> Sign in with SSO
                </a>
              )}
              {providers.saml && (
                <a
                  href={sso.loginURL("saml")}
                  className={`w-full flex items-center justify-center gap-2 py-3 text-sm font-medium rounded-lg transition-colors ${
                    providers.oidc
                      ? "text-slate-200 border border-slate-700 hover:bg-slate-800"
                      : "text-white bg-indigo-600 hover:bg-indigo-500"
                  }`}
                >
                  <LogIn className="w-4 h-4" aria-hidden="true" /> Sign in with SAML
                </a>
              )}
              <p className="text-center text-xs text-slate-300 pt-2">or connect with an API key</p>
            </div>
          )}

          <div className="space-y-3">
            <div className="relative">
              <label htmlFor="api-key-input" className="sr-only">
                API key
              </label>
              <input
                id="api-key-input"
                name="api-key"
                autoComplete="off"
                type={showKey ? "text" : "password"}
                className="w-full bg-slate-900 border border-slate-700 rounded-lg px-4 py-3 pr-10 text-sm font-mono text-slate-200 focus:outline-none focus:border-indigo-500 placeholder-slate-400"
                placeholder="vr_…"
                value={input}
                onChange={(e) => { setInput(e.target.value); setError(""); }}
                onKeyDown={(e) => e.key === "Enter" && handleConnect()}
                autoFocus={!providers}
              />
              <button
                type="button"
                onClick={() => setShowKey(!showKey)}
                aria-label={showKey ? "Hide API key" : "Show API key"}
                aria-pressed={showKey}
                className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-300 hover:text-slate-200"
              >
                {showKey ? <EyeOff className="w-4 h-4" aria-hidden="true" /> : <Eye className="w-4 h-4" aria-hidden="true" />}
              </button>
            </div>

            {(error || notice) && (
              <p role="alert" className={`text-xs ${error ? "text-red-300" : "text-amber-300"}`}>{error || notice}</p>
            )}

            <button
              onClick={handleConnect}
              disabled={testing || !input.trim()}
              className="w-full py-3 text-sm font-medium text-white bg-indigo-600 rounded-lg hover:bg-indigo-500 disabled:opacity-60 transition-colors"
            >
              {testing ? "Verifying…" : "Connect"}
            </button>
          </div>

          <p className="text-center text-xs text-slate-200">
            Generate a key with{" "}
            <code className="font-mono text-slate-300">make bootstrap-key</code>
          </p>
        </div>
      </div>
    );
  }

  return (
    <ApiKeyContext.Provider value={{ apiKey, setApiKey, clearApiKey, isSSO: apiKey === SSO_SESSION, isMaster, keyName, roleReady }}>
      {children}
    </ApiKeyContext.Provider>
  );
}
