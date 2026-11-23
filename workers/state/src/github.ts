import type { Env } from "./env";
import { type IdentityProvider, type Session, SignInError } from "./identity";
import { sha256 } from "./seal";

// GitHub sign-in: an OAuth app with the read:org scope. A user may sign in
// when they're an active member of GITHUB_ORG (and of GITHUB_TEAM, when set),
// and their token stops working as soon as they aren't: every state request
// re-checks membership with the user's GitHub token, cached for a minute.

const CHECK_TTL_MS = 60_000;

const checked = new Map<string, number>(); // sha256(token) -> allowed until (ms)

export const github: IdentityProvider = {
  enabled(env) {
    return !!(env.GITHUB_CLIENT_ID && env.GITHUB_CLIENT_SECRET && env.GITHUB_ORG);
  },

  authorizeUrl(env, callback, state) {
    const q = new URLSearchParams({
      client_id: env.GITHUB_CLIENT_ID ?? "",
      redirect_uri: callback,
      scope: "read:org",
      state,
      allow_signup: "false",
    });
    return `${web(env)}/login/oauth/authorize?${q}`;
  },

  async complete(env, request, callback) {
    const code = new URL(request.url).searchParams.get("code");
    if (!code) throw new SignInError("GitHub didn't return an authorization code.");
    const resp = await fetch(`${web(env)}/login/oauth/access_token`, {
      method: "POST",
      headers: { Accept: "application/json", "Content-Type": "application/json" },
      body: JSON.stringify({
        client_id: env.GITHUB_CLIENT_ID,
        client_secret: env.GITHUB_CLIENT_SECRET,
        code,
        redirect_uri: callback,
      }),
    });
    const tok = (await resp.json()) as { access_token?: string; error_description?: string };
    if (!tok.access_token) throw new SignInError(`GitHub sign-in failed: ${tok.error_description ?? resp.status}`);
    const user = await api<{ login: string }>(env, tok.access_token, "/user");
    if (!user) throw new SignInError("Couldn't read your GitHub account.");
    const session: Session = { subject: `github:${user.login}`, data: { token: tok.access_token, login: user.login } };
    if (!(await isMember(env, session))) {
      throw new SignInError(`${user.login} isn't an active member of ${scopeName(env)}.`);
    }
    return session;
  },

  async check(env, session) {
    const k = await sha256(session.data.token ?? "");
    const until = checked.get(k);
    if (until && until > Date.now()) return true;
    const ok = await isMember(env, session);
    if (ok) checked.set(k, Date.now() + CHECK_TTL_MS);
    else checked.delete(k);
    return ok;
  },
};

async function isMember(env: Env, session: Session): Promise<boolean> {
  const { token, login } = session.data;
  if (!token || !login || !env.GITHUB_ORG) return false;
  const org = await api<{ state: string }>(env, token, `/user/memberships/orgs/${encodeURIComponent(env.GITHUB_ORG)}`);
  if (org?.state !== "active") return false;
  if (!env.GITHUB_TEAM) return true;
  const team = await api<{ state: string }>(
    env,
    token,
    `/orgs/${encodeURIComponent(env.GITHUB_ORG)}/teams/${encodeURIComponent(env.GITHUB_TEAM)}/memberships/${encodeURIComponent(login)}`,
  );
  return team?.state === "active";
}

// GITHUB_URL and GITHUB_API_URL point at GitHub Enterprise Server instead.
function web(env: Env): string {
  return (env.GITHUB_URL || "https://github.com").replace(/\/$/, "");
}

function apiBase(env: Env): string {
  return (env.GITHUB_API_URL || "https://api.github.com").replace(/\/$/, "");
}

async function api<T>(env: Env, token: string, path: string): Promise<T | null> {
  const resp = await fetch(apiBase(env) + path, {
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "User-Agent": "baseplate-state-worker",
      "X-GitHub-Api-Version": "2022-11-28",
    },
  });
  if (!resp.ok) return null;
  return (await resp.json()) as T;
}

function scopeName(env: Env): string {
  return env.GITHUB_TEAM ? `${env.GITHUB_ORG}/${env.GITHUB_TEAM}` : `the ${env.GITHUB_ORG} org`;
}
