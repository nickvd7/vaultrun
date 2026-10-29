# SSO Setup Guide

> **Enterprise feature** — SSO ships in the separate VaultRun Enterprise
> repository and is compiled into the API server as an overlay
> (`go build -tags enterprise`). A core build refuses to start when
> `OIDC_*`/`SAML_*` env vars are set.
>
> **How to get Enterprise** — request evaluation, a production license, or a
> sales conversation at [vaultrun.dev/#enterprise](https://vaultrun.dev/#enterprise).
> Every request is emailed to **mail@030.dev**. Dev/test evaluation is free;
> production use requires a commercial license.

This guide walks through configuring **OIDC** or **SAML 2.0** single sign-on
for the VaultRun dashboard, end to end — from registering an application with
your identity provider (IdP) to verifying a successful login. Once configured,
the dashboard's sign-in screen shows a **Sign in with SSO** button; signed-in
users work in the dashboard with their session and can create a personal API
key for the CLI, SDKs and MCP. For a quick
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

1. **Use the dashboard's public URL.** The dashboard forwards `/auth/*` (and
   `/api/*`) to the API, so the IdP sends users back to the dashboard's own
   origin and the session cookie belongs to it. In this guide that is
   `https://vaultrun.example.com`. Callback URLs are derived from it and must
   be registered with your IdP *exactly* — trailing slashes and `http` vs
   `https` mismatches are the #1 cause of "redirect_uri_mismatch" errors.
   (Pointing them at the API's own host signs users in to the API, not the
   dashboard.)
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
4. **Configure Redis** (`REDIS_ADDR`, part of the standard compose stack) so
   that signing out invalidates the session server-side. Without Redis, a
   signed-out session stays valid until it expires
   (`SSO_SESSION_MAX_AGE_HOURS`, default 24h) for anyone holding a copy of the
   cookie; the dashboard says so when you sign out. When Redis is configured
   but unreachable, sessions are refused (VaultRun cannot check whether they
   were signed out) and signing out reports that the server could not revoke
   the session.
5. **Decide who may sign in** — see
   [Who may sign in, and with what access](#who-may-sign-in-and-with-what-access).
   By default every user of the IdP application can sign in, without access
   to secrets and without org roles.

---

## Option A — OIDC / OpenID Connect

Use this for Okta, Azure AD (Entra ID), Google Workspace, Keycloak, Auth0, or
any OpenID-Connect-compliant IdP.

### 1. Register an application with your IdP

Create a new **OIDC / OAuth2 "Web Application"** (not SPA, not native/mobile —
VaultRun performs the Authorization Code + PKCE flow server-side) with:

- **Sign-in redirect URI / Callback URL:**
  `https://vaultrun.example.com/auth/oidc/callback`
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
OIDC_REDIRECT_URL=https://vaultrun.example.com/auth/oidc/callback
OIDC_SCOPES=openid,email,profile

SSO_SESSION_SECRET=<output of `openssl rand -hex 32`>
```

Restart the server. On startup VaultRun performs OIDC discovery against
`OIDC_ISSUER_URL`; check the logs for `oidc discover` errors if it fails to
start.

### 3. Test the login flow

1. Open the dashboard (`https://vaultrun.example.com`) and click
   **Sign in with SSO** (or open `/auth/oidc/login` directly).
2. You should be redirected to your IdP's login page. Sign in.
3. The IdP redirects back to `/auth/oidc/callback`, VaultRun exchanges the
   authorization code for an ID token, verifies its signature against the
   IdP's published JWKS, applies the sign-in policy, provisions (or finds) an
   `sso_users` row + API key, and sets the `vaultrun_session` cookie.
4. You land on the dashboard (`SSO_POST_LOGIN_URL`, default `/dashboard`),
   signed in; the sidebar shows your `sso:oidc:<email>` identity.
5. **Sign out** in the sidebar revokes the session server-side (with Redis)
   and deletes the cookie.

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
                             issues the session cookie, redirects to
                             SSO_POST_LOGIN_URL
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
SAML_ROOT_URL=https://vaultrun.example.com   # the dashboard's public URL
```

> Set a calendar reminder for renewal — `-days 3650` is ten years, but if
> your organization has shorter rotation policies, regenerate and re-upload
> to your IdP before expiry.

### 2. Get your SP metadata to the IdP

Once `SAML_CERT_FILE`/`SAML_KEY_FILE`/`SAML_ROOT_URL` and a metadata source
(see step 3) are configured and the server is running, VaultRun serves SP
metadata at:

```
GET https://vaultrun.example.com/auth/saml/metadata
```

Give this URL — or the downloaded XML — to your IdP administrator when
registering VaultRun as a SAML application ("Service Provider" / "Relying
Party"). It contains the SP entity ID, ACS URL
(`https://vaultrun.example.com/auth/saml/acs`), and the SP's public certificate.

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
SAML_ROOT_URL=https://vaultrun.example.com
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

1. Open the dashboard and click **Sign in with SAML** (or open
   `https://vaultrun.example.com/auth/saml/login`).
2. You're redirected to your IdP's SSO page. Sign in.
3. The IdP POSTs a signed SAML assertion back to
   `https://vaultrun.example.com/auth/saml/acs`. VaultRun validates the
   signature, the `InResponseTo` value (against the AuthnRequest ID it
   stored when you started the login — this prevents replay of captured
   assertions), provisions the user, and sets the session cookie.
4. You land on the dashboard, signed in, as in the OIDC section above.

### How the flow works (for troubleshooting)

```
GET  /auth/saml/login  → generates a signed AuthnRequest, stores its ID in
                          a saml_request_id cookie (SameSite=Strict, 10 min),
                          redirects to the IdP's SSO endpoint
POST /auth/saml/acs    → requires Content-Type: application/x-www-form-urlencoded;
                          validates the assertion's signature and
                          InResponseTo against the stored request ID,
                          extracts NameID/email/name/group attributes,
                          applies the sign-in policy, upserts the
                          sso_users row, issues the session cookie
```

---

## Who may sign in, and with what access

Every sign-in goes through a policy, set with these variables:

| Variable | Default | Effect |
|---|---|---|
| `SSO_ALLOWED_DOMAINS` | *(any)* | Comma-separated e-mail domains that may sign in, e.g. `example.com`. |
| `SSO_ALLOWED_GROUPS` | *(any)* | Comma-separated IdP groups; the user must be in at least one. |
| `SSO_GROUPS_CLAIM` | `groups` | OIDC claim that lists the groups (SAML: `groups`, `memberOf` or the Microsoft/AD FS group claims). |
| `SSO_ROLE_MAPPING` | *(none)* | `group=org-slug:role,…` with role `viewer`, `executor` or `admin`. |
| `SSO_ALLOWED_SECRETS` | *(none)* | Secrets SSO users may inject into runs; `*` allows all. |
| `SSO_ALLOW_UNVERIFIED_EMAIL` | `false` | Accept an OIDC IdP that says `email_verified=false`. |
| `SSO_POST_LOGIN_URL` | `/dashboard` | Where the browser lands after signing in. |

- **Restrict sign-in.** Without `SSO_ALLOWED_DOMAINS` or `SSO_ALLOWED_GROUPS`
  every user the IdP lets into the application can sign in; the server logs a
  warning at startup. Refused sign-ins get a short explanation page and an
  `sso_login_denied` audit entry.
- **Verified addresses only.** An ID token with `email_verified: false` is
  refused. IdPs that do not send the claim (Azure AD) are accepted; combine
  them with `SSO_ALLOWED_DOMAINS` on a domain the IdP controls.
- **Org roles from groups.** Each org named in `SSO_ROLE_MAPPING` is managed by
  SSO: on every sign-in the user's membership is set to the highest role their
  groups give, or removed when none does. Create the orgs first
  (`POST /api/v1/orgs`); orgs not named in the mapping are left alone.
  Example: `SSO_ROLE_MAPPING=eng=platform:executor,platform-admins=platform:admin`.
- **No secrets by default.** SSO keys may not inject secrets unless
  `SSO_ALLOWED_SECRETS` names them (or is `*`). The setting is applied to every
  SSO key at each sign-in, so changing it takes effect on the next sign-in.

## Identity-to-API-key mapping

On first sign-in VaultRun creates a row in `sso_users` mapping the external
identity (`sub` claim for OIDC, `NameID` for SAML) to a freshly generated
VaultRun API key, named `sso:<provider>:<email>`. The session cookie is bound
to that key: everything the user does is recorded under it, and audit entries
(including `sso_login`) carry the key ID as the actor, never a name the IdP
chose. Subsequent sign-ins reuse the mapping and refresh
`last_login_at`/`name`/`email` and the key's name and allowed secrets.

- **Personal API key.** On the dashboard's **API Keys** page a signed-in user
  can create a personal API key for the CLI, SDKs and MCP. It is the same key
  their session uses, with a new secret: same identity, sessions and org
  roles. It is shown once; creating another one stops the previous one from
  working. (`POST /auth/api-key` with the session cookie.)
- **Blocking a user.** Revoke their key (`DELETE /api/v1/keys/:id`, or the
  API Keys page with the master key). Their session stops working at once
  and signing in again is refused with an explanation. Removing them from the
  IdP application or from `SSO_ALLOWED_GROUPS` works too.
- **Letting them back in.** Delete the revoked key
  (`DELETE /api/v1/keys/:id?purge=true`, or **Delete** on the API Keys page).
  The next sign-in issues a new key. Sessions created under the old key stay
  with the old key ID.

**Requests with the session cookie.** The API accepts the `vaultrun_session`
cookie wherever it accepts an API key. A request that changes something
(anything but `GET`/`HEAD`/`OPTIONS`) must also carry the header
`X-VaultRun-Client` (any value); the dashboard always sends it. Browsers attach
cookies to form posts from other sites, but those cannot add a custom header,
so this blocks cross-site request forgery. `POST /auth/logout` requires the
header too.

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
| `GET /auth/me` returns 401 despite a valid-looking cookie | The session was revoked (e.g. you logged out elsewhere and Redis is configured), Redis is configured but unreachable (sessions are refused while it is down), the cookie's signature doesn't match `SSO_SESSION_SECRET` (secret changed/rotated), or the JWT expired |
| "Sign-in refused" page after the IdP | The sign-in policy refused the user: e-mail domain or group not allowed, `email_verified=false`, or an administrator revoked their key (see [Identity-to-API-key mapping](#identity-to-api-key-mapping)). The audit log has an `sso_login_denied` entry with the reason |
| Signed in at the IdP, but the dashboard shows its sign-in screen again | The callback URL points at the API's host instead of the dashboard's, so the cookie belongs to the API origin. Use the dashboard's public URL in `OIDC_REDIRECT_URL` / `SAML_ROOT_URL` |
| `403` "must carry the X-VaultRun-Client header" | A script used the session cookie for a write request; send the header, or use a personal API key instead |
| Sign-in fails after the IdP rotated its signing key | VaultRun refetches the IdP's keys when a token is signed with a key it does not know (at most every 30 s), so this should resolve itself; check the logs for `id_token verification failed` |

For the full security model behind these flows (replay protection, JWKS
verification, session revocation, etc.), see
[docs/security.md](security.md).
