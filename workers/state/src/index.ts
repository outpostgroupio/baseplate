// Baseplate remote state server for Cloudflare Workers.
//
// It speaks the Terraform/OpenTofu http backend protocol, so Baseplate's
// `backend "http"` (and Terraform's) works with it unchanged: state is read
// with GET and written with POST, and locked with LOCK/UNLOCK (or POST/DELETE
// on <state>/lock). State is stored in R2; each state's lock lives in a
// Durable Object. Users sign in with `baseplate login` (GitHub by default; see
// provider.ts), CI uses GitHub Actions OIDC, and ADMIN_TOKEN is for
// emergencies. docs/state-server-protocol.md in the Baseplate repo describes
// the protocol.

import type { Env } from "./env";
import { verifyActionsToken, ISSUER } from "./oidc";
import { type Session, SignInError } from "./identity";
import { identityProvider } from "./provider";
import { now, safeEqual, seal, sha256, unseal } from "./seal";
import { type LockInfo, type Result, validName } from "./state";

export { StateObject } from "./state";

/** The client id the Baseplate CLI uses, as in Terraform's login.v1. */
const CLIENT_ID = "baseplate-cli";
/** Localhost ports the CLI may listen on for the sign-in redirect. */
const PORTS: [number, number] = [10000, 10010];
const TOKEN_PREFIX = "bp1.";

interface AuthzState {
  typ: "authz";
  exp: number;
  redirect: string;
  state: string;
  challenge: string;
}
interface Code {
  typ: "code";
  exp: number;
  redirect: string;
  challenge: string;
  session: Session;
}
interface AccessToken {
  typ: "access";
  exp: number;
  session: Session;
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    try {
      return await route(request, env);
    } catch (err) {
      console.error(err);
      return text(500, "internal error");
    }
  },
} satisfies ExportedHandler<Env>;

async function route(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  const path = url.pathname;
  if (path === "/.well-known/baseplate.json" && request.method === "GET") return discovery(env);
  if (path === "/oauth/authorize" && request.method === "GET") return authorize(request, env);
  if (path === "/oauth/callback" && request.method === "GET") return callback(request, env);
  if (path === "/oauth/token" && request.method === "POST") return token(request, env);
  if (path.startsWith("/states/")) return states(request, env, path.slice("/states/".length));
  if (path === "/") return text(200, "Baseplate state server. See https://github.com/outpostgroupio/baseplate.\n");
  return text(404, "not found");
}

function discovery(env: Env): Response {
  const doc: Record<string, unknown> = {};
  if (identityProvider.enabled(env)) {
    doc["login.v1"] = {
      client: CLIENT_ID,
      grant_types: ["authz_code"],
      authz: "/oauth/authorize",
      token: "/oauth/token",
      ports: PORTS,
    };
  }
  if (repositories(env).length > 0) doc["oidc.v1"] = { issuers: [ISSUER] };
  return json(200, doc);
}

// --- Sign-in (OAuth 2.0 authorization code + PKCE, as `terraform login`) ---

async function authorize(request: Request, env: Env): Promise<Response> {
  if (!identityProvider.enabled(env)) return text(404, "sign-in is not configured on this server");
  const q = new URL(request.url).searchParams;
  const redirect = q.get("redirect_uri") ?? "";
  if (!loopbackRedirect(redirect)) return text(400, "redirect_uri must be http://127.0.0.1 or http://localhost on a login.v1 port");
  const state = q.get("state") ?? "";
  const challenge = q.get("code_challenge") ?? "";
  if (q.get("response_type") !== "code" || q.get("client_id") !== CLIENT_ID) return back(redirect, state, "unsupported_response_type");
  if (q.get("code_challenge_method") !== "S256" || challenge.length < 43 || !state) return back(redirect, state, "invalid_request");
  const sealed = await seal(env.SESSION_SECRET, { typ: "authz", exp: now() + 600, redirect, state, challenge } satisfies AuthzState);
  return Response.redirect(identityProvider.authorizeUrl(env, callbackUrl(request), sealed), 302);
}

async function callback(request: Request, env: Env): Promise<Response> {
  const q = new URL(request.url).searchParams;
  const authz = await unseal<AuthzState>(env.SESSION_SECRET, q.get("state") ?? "", "authz");
  if (!authz) return text(400, "This sign-in link has expired or is invalid. Run baseplate login again.");
  let session: Session;
  try {
    session = await identityProvider.complete(env, request, callbackUrl(request));
  } catch (err) {
    if (err instanceof SignInError) return back(authz.redirect, authz.state, "access_denied", err.message);
    throw err;
  }
  const code = await seal(env.SESSION_SECRET, {
    typ: "code",
    exp: now() + 60,
    redirect: authz.redirect,
    challenge: authz.challenge,
    session,
  } satisfies Code);
  const to = new URL(authz.redirect);
  to.searchParams.set("code", code);
  to.searchParams.set("state", authz.state);
  return Response.redirect(to.toString(), 302);
}

async function token(request: Request, env: Env): Promise<Response> {
  let form: FormData;
  try {
    form = await request.formData();
  } catch {
    return oauthError("invalid_request", "expected a form-encoded body");
  }
  const field = (k: string) => (typeof form.get(k) === "string" ? (form.get(k) as string) : "");
  if (field("grant_type") !== "authorization_code") return oauthError("unsupported_grant_type");
  if (field("client_id") !== CLIENT_ID) return oauthError("invalid_client");
  const code = await unseal<Code>(env.SESSION_SECRET, field("code"), "code");
  if (!code || code.redirect !== field("redirect_uri")) return oauthError("invalid_grant");
  // PKCE: only the CLI that started the sign-in knows the verifier.
  if ((await sha256(field("code_verifier"))) !== code.challenge) return oauthError("invalid_grant", "code_verifier doesn't match");
  const ttl = Math.max(1, Number(env.TOKEN_TTL_DAYS) || 30) * 86400;
  const access = await seal(env.SESSION_SECRET, { typ: "access", exp: now() + ttl, session: code.session } satisfies AccessToken);
  return json(200, { access_token: TOKEN_PREFIX + access, token_type: "bearer", expires_in: ttl });
}

function callbackUrl(request: Request): string {
  return new URL("/oauth/callback", request.url).toString();
}

function loopbackRedirect(raw: string): boolean {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return false;
  }
  const port = Number(u.port);
  return u.protocol === "http:" && (u.hostname === "127.0.0.1" || u.hostname === "localhost") && port >= PORTS[0] && port <= PORTS[1];
}

function back(redirect: string, state: string, error: string, description?: string): Response {
  const to = new URL(redirect);
  to.searchParams.set("error", error);
  if (description) to.searchParams.set("error_description", description);
  if (state) to.searchParams.set("state", state);
  return Response.redirect(to.toString(), 302);
}

function oauthError(error: string, description?: string): Response {
  return json(400, description ? { error, error_description: description } : { error });
}

// --- Authentication of state requests ---

/** Returns who is calling, or null. Accepts a bearer token, or the password of HTTP Basic auth (for Terraform/OpenTofu clients). */
export async function authenticate(request: Request, env: Env): Promise<string | null> {
  const header = request.headers.get("Authorization") ?? "";
  let tok = "";
  if (/^bearer /i.test(header)) tok = header.slice(7).trim();
  else if (/^basic /i.test(header)) {
    try {
      const decoded = atob(header.slice(6).trim());
      tok = decoded.slice(decoded.indexOf(":") + 1);
    } catch {
      return null;
    }
  }
  if (!tok) return null;
  if (env.ADMIN_TOKEN && safeEqual(tok, env.ADMIN_TOKEN)) return "admin";
  if (tok.startsWith(TOKEN_PREFIX)) {
    const access = await unseal<AccessToken>(env.SESSION_SECRET, tok.slice(TOKEN_PREFIX.length), "access");
    if (!access || !(await identityProvider.check(env, access.session))) return null;
    return access.session.subject;
  }
  const repos = repositories(env);
  if (repos.length > 0 && tok.split(".").length === 3) {
    return verifyActionsToken(tok, new URL(request.url).origin, repos);
  }
  return null;
}

function repositories(env: Env): string[] {
  return (env.OIDC_REPOSITORIES ?? "")
    .split(",")
    .map((r) => r.trim().toLowerCase())
    .filter(Boolean);
}

// --- State (the http backend protocol) ---

async function states(request: Request, env: Env, rest: string): Promise<Response> {
  const who = await authenticate(request, env);
  if (!who) return new Response("unauthorized\n", { status: 401, headers: { "WWW-Authenticate": 'Bearer realm="baseplate-state"' } });

  // <name> is the state; <name>/lock also accepts POST (lock) and DELETE
  // (unlock), for clients configured GitLab-style.
  let name = rest;
  let lockPath = false;
  if (rest.endsWith("/lock")) {
    name = rest.slice(0, -"/lock".length);
    lockPath = true;
  }
  if (!validName(name)) return text(400, "invalid state name");
  const obj = env.STATE_OBJECTS.get(env.STATE_OBJECTS.idFromName(name));

  const method = request.method.toUpperCase();
  if (method === "LOCK" || (lockPath && method === "POST")) {
    const info = await lockBody(request);
    if (!info) return text(400, "lock body must be JSON with an ID");
    // Record who took the lock as the server saw them, so a blocked run's
    // error names the real holder.
    info.Who = info.Who ? `${info.Who} (${who})` : who;
    return lockResponse(await obj.lock(info), 423);
  }
  if (method === "UNLOCK" || (lockPath && method === "DELETE")) {
    const info = await lockBody(request);
    if (!info) return text(400, "unlock body must be JSON with the lock's ID");
    return lockResponse(await obj.unlock(info.ID), 409);
  }
  if (lockPath) return text(405, "method not allowed");
  if (method === "GET") {
    const body = await obj.getState(name);
    return body === null ? new Response(null, { status: 204 }) : new Response(body, { headers: { "Content-Type": "application/json" } });
  }
  if (method === "POST" || method === "PUT") {
    const body = await request.text();
    try {
      JSON.parse(body);
    } catch {
      return text(400, "state must be JSON");
    }
    const id = new URL(request.url).searchParams.get("ID");
    return lockResponse(await obj.putState(name, body, id), 409);
  }
  return text(405, "method not allowed");
}

async function lockBody(request: Request): Promise<LockInfo | null> {
  try {
    const info = (await request.json()) as LockInfo;
    return info && typeof info.ID === "string" && info.ID ? info : null;
  } catch {
    return null;
  }
}

function lockResponse(r: Result, conflictStatus: number): Response {
  if (!r.ok) return json(conflictStatus, r.lock);
  return r.lock ? json(200, r.lock) : new Response(null, { status: 200 });
}

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function text(status: number, body: string): Response {
  return new Response(body.endsWith("\n") ? body : body + "\n", { status, headers: { "Content-Type": "text/plain; charset=utf-8" } });
}
