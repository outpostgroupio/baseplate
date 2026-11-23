import { env } from "cloudflare:test";
import { afterEach, describe, expect, it, vi } from "vitest";
import { seal, sha256 } from "../src/seal";
import { call } from "./helpers";

const REDIRECT = "http://127.0.0.1:10003/login";
const VERIFIER = "v".repeat(43);

// Fakes GitHub: the OAuth token exchange, /user, and org membership. Members
// are the logins in `members`.
function fakeGitHub(members: string[]) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const auth = new Headers(init?.headers).get("Authorization") ?? "";
    const login = auth.replace("Bearer gh-token-", "");
    if (url === "https://github.com/login/oauth/access_token") {
      const body = JSON.parse(String(init?.body));
      return Response.json({ access_token: `gh-token-${body.code}` });
    }
    if (url === "https://api.github.com/user") return Response.json({ login });
    if (url === "https://api.github.com/user/memberships/orgs/acme") {
      return members.includes(login) ? Response.json({ state: "active" }) : new Response("", { status: 404 });
    }
    return new Response("unexpected " + url, { status: 500 });
  });
}

async function signIn(user: string): Promise<Response> {
  const q = new URLSearchParams({
    response_type: "code",
    client_id: "baseplate-cli",
    redirect_uri: REDIRECT,
    state: "cli-state",
    code_challenge: await sha256(VERIFIER),
    code_challenge_method: "S256",
  });
  const authz = await call(`/oauth/authorize?${q}`);
  expect(authz.status).toBe(302);
  const gh = new URL(authz.headers.get("Location")!);
  expect(gh.origin + gh.pathname).toBe("https://github.com/login/oauth/authorize");
  expect(gh.searchParams.get("redirect_uri")).toBe("https://state.example.com/oauth/callback");
  // GitHub sends the browser back with its code (the user's login, in this fake).
  return call(`/oauth/callback?code=${user}&state=${encodeURIComponent(gh.searchParams.get("state")!)}`);
}

function exchange(code: string, verifier = VERIFIER): Promise<Response> {
  return call("/oauth/token", {
    method: "POST",
    body: new URLSearchParams({ grant_type: "authorization_code", client_id: "baseplate-cli", code, code_verifier: verifier, redirect_uri: REDIRECT }),
  });
}

afterEach(() => vi.restoreAllMocks());

describe("baseplate login", () => {
  it("advertises login.v1 and oidc.v1", async () => {
    const doc = (await (await call("/.well-known/baseplate.json")).json()) as Record<string, any>;
    expect(doc["login.v1"]).toMatchObject({ client: "baseplate-cli", grant_types: ["authz_code"], ports: [10000, 10010] });
    expect(doc["oidc.v1"]).toBeDefined();
  });

  it("signs in an org member and the token opens the state", async () => {
    fakeGitHub(["josh"]);
    const back = await signIn("josh");
    expect(back.status).toBe(302);
    const to = new URL(back.headers.get("Location")!);
    expect(to.origin + to.pathname).toBe(REDIRECT);
    expect(to.searchParams.get("state")).toBe("cli-state");

    const tok = await exchange(to.searchParams.get("code")!);
    expect(tok.status).toBe(200);
    const { access_token } = (await tok.json()) as { access_token: string };
    expect(access_token.startsWith("bp1.")).toBe(true);

    const auth = { Authorization: `Bearer ${access_token}` };
    expect((await call("/states/login-ok", { headers: auth })).status).toBe(204);
    await call("/states/login-ok", { method: "LOCK", headers: auth, body: JSON.stringify({ ID: "x", Who: "josh@mac" }) });
    const blocked = await call("/states/login-ok", { method: "LOCK", headers: auth, body: JSON.stringify({ ID: "y" }) });
    expect(((await blocked.json()) as { Who: string }).Who).toBe("josh@mac (github:josh)");
  });

  it("turns away someone outside the org", async () => {
    fakeGitHub([]);
    const back = await signIn("mallory");
    const to = new URL(back.headers.get("Location")!);
    expect(to.searchParams.get("error")).toBe("access_denied");
    expect(to.searchParams.get("code")).toBeNull();
  });

  it("rejects a code exchanged without the PKCE verifier", async () => {
    fakeGitHub(["josh"]);
    const to = new URL((await signIn("josh")).headers.get("Location")!);
    const tok = await exchange(to.searchParams.get("code")!, "w".repeat(43));
    expect(tok.status).toBe(400);
  });

  it("refuses a redirect that isn't a loopback login port", async () => {
    const q = new URLSearchParams({
      response_type: "code",
      client_id: "baseplate-cli",
      redirect_uri: "https://evil.example.com/login",
      state: "s",
      code_challenge: await sha256(VERIFIER),
      code_challenge_method: "S256",
    });
    expect((await call(`/oauth/authorize?${q}`)).status).toBe(400);
  });

  it("stops accepting a token once the user leaves the org", async () => {
    fakeGitHub([]); // no longer a member
    const sealed = await seal(env.SESSION_SECRET, {
      typ: "access",
      exp: Math.floor(Date.now() / 1000) + 3600,
      session: { subject: "github:gone", data: { token: "gh-token-gone", login: "gone" } },
    });
    expect((await call("/states/left", { headers: { Authorization: `Bearer bp1.${sealed}` } })).status).toBe(401);
  });

  it("doesn't accept a sign-in code as an access token", async () => {
    fakeGitHub(["josh"]);
    const to = new URL((await signIn("josh")).headers.get("Location")!);
    const code = to.searchParams.get("code")!;
    expect((await call("/states/x", { headers: { Authorization: `Bearer bp1.${code}` } })).status).toBe(401);
  });
});

describe("token endpoint", () => {
  it("answers a malformed body with an OAuth error", async () => {
    const r = await call("/oauth/token", { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" });
    expect(r.status).toBe(400);
    expect(((await r.json()) as { error: string }).error).toBe("invalid_request");
  });
});
