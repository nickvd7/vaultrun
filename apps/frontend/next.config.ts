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

const nextConfig: NextConfig = {
  output: "standalone",
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
