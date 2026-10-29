import type { NextConfig } from "next";
import pkg from "./package.json";

// The browser talks to the dashboard origin only; /api/* is forwarded to the
// VaultRun API from the server side. This avoids CORS and keeps the API port
// out of the browser's concerns. Next evaluates rewrites at build time, so the
// upstream is a build argument (see Dockerfile / docker-compose.yml).
// Requests carry the key the user entered in the dashboard; the dashboard adds
// no credentials of its own. /auth/* (single sign-on, VaultRun Enterprise) is
// forwarded too, so the IdP redirects back to the dashboard's own origin and
// the session cookie belongs to it.
const apiUpstream = (process.env.DASHBOARD_API_UPSTREAM || "http://localhost:8080").replace(/\/$/, "");

// The dashboard holds an API key or session, so it must not be framed
// (clickjacking) and should only run its own scripts. Next.js inlines its
// bootstrap scripts, hence 'unsafe-inline'; no third-party origin is allowed.
// A separate API origin (NEXT_PUBLIC_API_URL) is the only extra connect-src.
const apiOrigin = (process.env.NEXT_PUBLIC_API_URL || "").replace(/\/$/, "");
const csp = [
  "default-src 'self'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  `connect-src 'self'${apiOrigin ? " " + apiOrigin : ""}`,
  "object-src 'none'",
  "base-uri 'self'",
  "form-action 'self'",
  "frame-ancestors 'none'",
].join("; ");

const securityHeaders = [
  { key: "Content-Security-Policy", value: csp },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "Referrer-Policy", value: "no-referrer" },
  { key: "Permissions-Policy", value: "camera=(), microphone=(), geolocation=(), payment=()" },
  // Ignored over plain HTTP; applies once the dashboard is served over TLS.
  { key: "Strict-Transport-Security", value: "max-age=31536000; includeSubDomains" },
];

const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  // The dashboard serves no remote images; turning the optimizer off removes
  // the /_next/image endpoint and its attack surface.
  images: { unoptimized: true },
  async headers() {
    return [{ source: "/:path*", headers: securityHeaders }];
  },
  env: {
    NEXT_PUBLIC_APP_VERSION: pkg.version,
  },
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${apiUpstream}/api/:path*`,
      },
      {
        source: "/auth/:path*",
        destination: `${apiUpstream}/auth/:path*`,
      },
    ];
  },
};

export default nextConfig;
