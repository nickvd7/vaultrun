# SSO Setup Guide

> **Enterprise feature** — SSO ships in the separate VaultRun Enterprise
> repository and is compiled into the API server as an overlay
> (`go build -tags enterprise`). A core build refuses to start when
> `OIDC_*`/`SAML_*` env vars are set. For access and licensing,
> contact **mail@030.dev**.

This guide walks through configuring **OIDC** or **SAML 2.0** single sign-on
for the VaultRun dashboard, end to end — from registering an application with
your identity provider (IdP) to verifying a successful login. For a quick
reference of every environment variable, see
[docs/configuration.md](configuration.md#sso--oidc--openid-connect).

SSO logins auto-provision a VaultRun API key behind the scenes and issue a
signed session cookie (`vaultrun_session`). They never grant master-key
privileges — an SSO user is exactly as powerful as the API key VaultRun
creates for them.

Pick **one** of OIDC or SAML based on what your IdP/organization supports —
running both at once is fine, but most deployments only need one.

---

## Before you start

1. **Decide on your public URL.** Both protocols need to know the externally
   reachable base URL of your VaultRun API server, e.g.
   `https://api.example.com`. SSO redirect/callback URLs are derived from it
   and must be registered with your IdP *exactly* — trailing slashes and
   `http` vs `https` mismatches are the #1 cause of "redirect_uri_mismatch"
   errors.
2. **Run behind TLS in production.** Session cookies are marked `Secure` by
   default whenever TLS is active (`SSO_SESSION_SECURE`); browsers will not
   send `Secure` cookies over plain HTTP, so SSO logins will appear to "not
   stick" if you serve the callback over HTTP.
3. **Generate a session secret** (used to sign session JWTs, independent of
   your IdP choice):
   ```bash
   openssl rand -hex 32
   ```
   Set this as `SSO_SESSION_SECRET`. It must be at least 32 bytes — the
   server refuses to start with a shorter secret.
4. *(Optional but recommended)* **Configure Redis** (`REDIS_ADDR`) so that
   logging out immediately invalidates the session server-side. Without
   Redis, a logged-out session JWT remains technically valid (though no
   longer sent by the browser) until it naturally expires
   (`SSO_SESSION_MAX_AGE_HOURS`, default 24h).

---

## Option A — OIDC / OpenID Connect

Use this for Okta, Azure AD (Entra ID), Google Workspace, Keycloak, Auth0, or
any OpenID-Connect-compliant IdP.

### 1. Register an application with your IdP

Create a new **OIDC / OAuth2 "Web Application"** (not SPA, not native/mobile —
VaultRun performs the Authorization Code + PKCE flow server-side) with:

- **Sign-in redirect URI / Callback URL:**
  `https://api.example.com/auth/oidc/callback`
- **Grant type:** Authorization Code (with PKCE if your IdP requires you to
  enable it explicitly — VaultRun always uses PKCE)
- **Scopes:** `openid email profile` (the defaults VaultRun requests)

After creation, note down the **Client ID**, **Client Secret**, and the
**issuer URL**. The issuer is the base URL where
`/.well-known/openid-configuration` is served — common values:

| IdP | Issuer URL |
|---|---|
| Okta | `https://<your-org>.okta.com/oauth2/default` (or a custom auth server URL) |
| Azure AD / Entra ID | `https://login.microsoftonline.com/<tenant-id>/v2.0` |
| Google Workspace | `https://accounts.google.com` |
| Keycloak | `https://keycloak.example.com/realms/<realm>` |
| Auth0 | `https://<your-tenant>.us.auth0.com/` |

> **Tip:** you can sanity-check the issuer URL by opening
> `<issuer>/.well-known/openid-configuration` in a browser before configuring
> VaultRun — it should return a JSON document. VaultRun performs this same
> discovery request at startup and **refuses to start** if it fails or the
> document is missing `authorization_endpoint`, `token_endpoint`, or
> `jwks_uri` (the latter is required to verify ID token signatures).

### 2. Configure VaultRun

Set the following in your environment (`.env` / Docker Compose / systemd unit):

```bash
OIDC_ISSUER_URL=https://your-org.okta.com/oauth2/default
OIDC_CLIENT_ID=<client id from your IdP>
OIDC_CLIENT_SECRET=<client secret from your IdP>
OIDC_REDIRECT_URL=https://api.example.com/auth/oidc/callback
OIDC_SCOPES=openid,email,profile

SSO_SESSION_SECRET=<output of `openssl rand -hex 32`>
```

Restart the server. On startup VaultRun performs OIDC discovery against
`OIDC_ISSUER_URL`; check the logs for `oidc discover` errors if it fails to
start.

### 3. Test the login flow

1. Open `https://api.example.com/auth/oidc/login` in a browser.
2. You should be redirected to your IdP's login page. Sign in.
3. The IdP redirects back to `/auth/oidc/callback`, VaultRun exchanges the
   authorization code for an ID token, verifies its signature against the
   IdP's published JWKS, provisions (or finds) an `sso_users` row + API key,
   and sets the `vaultrun_session` cookie.
4. You're redirected to `/`. Confirm the session is active:
   ```bash
   curl -s https://api.example.com/auth/me \
     --cookie "vaultrun_session=<value from browser dev tools>"
   ```
   This should return your email, name, and provider (`"oidc"`).
5. `POST /auth/logout` clears the cookie (and, with Redis configured, revokes
   the session server-side immediately).

### How the flow works (for troubleshooting)

```
GET  /auth/oidc/login     → sets oidc_state / oidc_verifier / oidc_nonce
                             cookies (SameSite=Strict, 10 min TTL),
                             redirects to the IdP's authorization endpoint
                             with PKCE challenge + state + nonce
GET  /auth/oidc/callback  → validates state (CSRF), exchanges code for an
                             ID token using the PKCE verifier, verifies the
                             token's signature/issuer/audience/nonce against
                             the IdP's JWKS, upserts the sso_users row,
                             issues the session cookie, redirects to /
```

---

## Option B — SAML 2.0

Use this for enterprise IdPs that don't support OIDC, or where your
organization standardizes on SAML (Okta, AD FS, OneLogin, PingFederate, etc.).

### 1. Generate a Service Provider certificate

VaultRun acts as the SAML **Service Provider (SP)** and needs an RSA
key pair to sign/decrypt SAML messages. A self-signed certificate is fine —
the IdP only needs the public certificate to verify SP-signed requests:

```bash
openssl req -x509 -newkey rsa:2048 -keyout saml.key -out saml.crt -days 3650 -nodes \
  -subj "/CN=vaultrun-sp"
```

Store both files somewhere VaultRun can read them, e.g.
`/etc/vaultrun/saml.{crt,key}`, and set:

```bash
SAML_CERT_FILE=/etc/vaultrun/saml.crt
SAML_KEY_FILE=/etc/vaultrun/saml.key
SAML_ROOT_URL=https://api.example.com
```

> Set a calendar reminder for renewal — `-days 3650` is ten years, but if
> your organization has shorter rotation policies, regenerate and re-upload
> to your IdP before expiry.

### 2. Get your SP metadata to the IdP

Once `SAML_CERT_FILE`/`SAML_KEY_FILE`/`SAML_ROOT_URL` and a metadata source
(see step 3) are configured and the server is running, VaultRun serves SP
metadata at:

```
GET https://api.example.com/auth/saml/metadata
```

Give this URL — or the downloaded XML — to your IdP administrator when
registering VaultRun as a SAML application ("Service Provider" / "Relying
Party"). It contains the SP entity ID, ACS URL
(`https://api.example.com/auth/saml/acs`), and the SP's public certificate.

The **SP Entity ID** defaults to `<SAML_ROOT_URL>/auth/saml/metadata`
(override with `SAML_ENTITY_ID` if your IdP requires a specific value).

### 3. Get the IdP's metadata into VaultRun

VaultRun needs the IdP's metadata XML to validate incoming SAML responses
(it contains the IdP's signing certificate and SSO endpoint). **Two options:**

**Option A — local file (recommended for production):**
Download the metadata XML once from your IdP's admin console and store it
locally:
```bash
SAML_IDP_METADATA_FILE=/etc/vaultrun/idp-metadata.xml
```
A pinned local file avoids a live network fetch on every server restart and
eliminates the (small) MITM risk of fetching metadata over the network at
runtime. **This is preferred whenever you can obtain the file.**

**Option B — live URL (fallback):**
```bash
SAML_IDP_METADATA_URL=https://your-org.okta.com/app/<app-id>/sso/saml/metadata
```
VaultRun fetches and parses this once at startup. If both are set, the local
file always takes precedence.

### 4. Configure VaultRun

```bash
SAML_ROOT_URL=https://api.example.com
SAML_CERT_FILE=/etc/vaultrun/saml.crt
SAML_KEY_FILE=/etc/vaultrun/saml.key
SAML_IDP_METADATA_FILE=/etc/vaultrun/idp-metadata.xml   # preferred
# SAML_IDP_METADATA_URL=https://...                     # or this, as a fallback
SAML_ENTITY_ID=                                          # leave empty to use the default

SSO_SESSION_SECRET=<output of `openssl rand -hex 32`>
```

Restart the server — it loads and parses the IdP metadata at startup and
**refuses to start** if the certificate/key can't be loaded or the metadata
can't be parsed.

### 5. Test the login flow

1. Open `https://api.example.com/auth/saml/login` in a browser.
2. You're redirected to your IdP's SSO page. Sign in.
3. The IdP POSTs a signed SAML assertion back to
   `https://api.example.com/auth/saml/acs`. VaultRun validates the
   signature, the `InResponseTo` value (against the AuthnRequest ID it
   stored when you started the login — this prevents replay of captured
   assertions), provisions the user, and sets the session cookie.
4. You're redirected to `/`. Verify with `GET /auth/me` as in the OIDC
   section above.

### How the flow works (for troubleshooting)

```
GET  /auth/saml/login  → generates a signed AuthnRequest, stores its ID in
                          a saml_request_id cookie (SameSite=Strict, 10 min),
                          redirects to the IdP's SSO endpoint
POST /auth/saml/acs    → requires Content-Type: application/x-www-form-urlencoded;
                          validates the assertion's signature and
                          InResponseTo against the stored request ID,
                          extracts NameID/email/name attributes, upserts
                          the sso_users row, issues the session cookie
```

---

## Identity-to-API-key mapping

On first login VaultRun creates a row in `sso_users` mapping the external
identity (`sub` claim for OIDC, `NameID` for SAML) to a freshly generated
VaultRun API key, named `sso:<provider>:<email>`. Subsequent logins reuse the
same mapping and refresh `last_login_at`/`name`/`email`. If the underlying API
key was later revoked or deleted by an administrator, the next SSO login
transparently reissues a new one and re-links it.

You can inspect provisioned SSO users via the regular `api_keys` /
`sso_users` tables, or revoke their access entirely with
`DELETE /api/v1/keys/:id` (using the key ID returned from `GET /auth/me` →
look up the corresponding `api_keys` row, since `/auth/me` intentionally does
not expose the raw key ID).

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Server refuses to start with `oidc discover` / `saml: load IdP metadata` errors | Issuer URL unreachable, metadata URL/file invalid, or the discovery document is missing `jwks_uri` |
| Server refuses to start: "session secret too short" | `SSO_SESSION_SECRET` is shorter than 32 bytes — regenerate with `openssl rand -hex 32` |
| `redirect_uri_mismatch` / `invalid_redirect_uri` from the IdP | `OIDC_REDIRECT_URL` doesn't *exactly* match what's registered with the IdP (scheme, host, trailing slash) |
| `400 invalid state` on OIDC callback | The `oidc_state`/`oidc_verifier`/`oidc_nonce` cookies expired (>10 min between login and callback) or were blocked by a browser privacy setting / proxy that strips cookies |
| `400 invalid SAML response` on ACS | Clock skew between VaultRun's host and the IdP (SAML assertions have tight `NotBefore`/`NotOnOrAfter` windows — keep both NTP-synced), wrong IdP metadata (stale signing certificate), or `InResponseTo` mismatch (assertion replay or a stale `saml_request_id` cookie) |
| `415 Unsupported Media Type` on ACS | The IdP is posting the assertion with a Content-Type other than `application/x-www-form-urlencoded` — check that the IdP is configured for the **HTTP-POST** binding |
| Session cookie not present after a successful-looking redirect | Running over plain HTTP while `SSO_SESSION_SECURE=true` (the default under TLS) — browsers silently drop `Secure` cookies sent over HTTP. Either terminate TLS in front of VaultRun, or explicitly set `SSO_SESSION_SECURE=false` for local development only |
| `GET /auth/me` returns 401 despite a valid-looking cookie | The session was revoked (e.g. you logged out elsewhere and Redis is configured), the cookie's signature doesn't match `SSO_SESSION_SECRET` (secret changed/rotated), or the JWT expired |

For the full security model behind these flows (replay protection, JWKS
verification, session revocation, etc.), see
[docs/security.md](security.md).
