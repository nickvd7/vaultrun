// Server-side proxy — forwards /api/proxy/** to the VaultRun API using a
// server-only API key. Prevents VAULTRUN_API_KEY from leaking to the browser.
import { NextRequest, NextResponse } from "next/server";

const UPSTREAM = (process.env.VAULTRUN_API_URL ?? "http://localhost:8080").replace(/\/$/, "");
const API_KEY  = process.env.VAULTRUN_API_KEY ?? "";

async function proxy(
  req: NextRequest,
  params: { path: string[] },
): Promise<NextResponse> {
  const upstreamURL = `${UPSTREAM}/${params.path.join("/")}${req.nextUrl.search}`;
  const contentType = req.headers.get("content-type") ?? "";
  const isMultipart = contentType.includes("multipart/form-data");

  const headers: Record<string, string> = { "X-API-Key": API_KEY };
  if (!isMultipart) headers["Content-Type"] = "application/json";

  const init: RequestInit = { method: req.method, headers };

  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = isMultipart ? await req.formData() : await req.text() || undefined;
  }

  const upstream = await fetch(upstreamURL, init);

  if (upstream.status === 204) return new NextResponse(null, { status: 204 });

  const respType = upstream.headers.get("content-type") ?? "";
  if (respType.includes("application/json")) {
    return NextResponse.json(await upstream.json(), { status: upstream.status });
  }

  // Binary (file download)
  return new NextResponse(await upstream.blob(), {
    status: upstream.status,
    headers: {
      "Content-Type": respType || "application/octet-stream",
      "Content-Disposition": upstream.headers.get("content-disposition") ?? "",
    },
  });
}

type RouteContext = { params: Promise<{ path: string[] }> };

export async function GET(req: NextRequest, ctx: RouteContext) {
  return proxy(req, await ctx.params);
}
export async function POST(req: NextRequest, ctx: RouteContext) {
  return proxy(req, await ctx.params);
}
export async function DELETE(req: NextRequest, ctx: RouteContext) {
  return proxy(req, await ctx.params);
}
export async function PATCH(req: NextRequest, ctx: RouteContext) {
  return proxy(req, await ctx.params);
}
