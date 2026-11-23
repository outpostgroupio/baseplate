import { fromBase64url } from "./seal";

// GitHub Actions OIDC: a workflow with `permissions: id-token: write` gets a
// signed JWT from GitHub that names its repository. Baseplate requests one
// with the Worker's origin as the audience and sends it as a bearer token, so
// CI needs no stored secret. The Worker checks the signature against GitHub's
// published keys, the issuer, the audience, the expiry and the repository.

export const ISSUER = "https://token.actions.githubusercontent.com";
const JWKS_URL = `${ISSUER}/.well-known/jwks`;
const JWKS_TTL_MS = 60 * 60 * 1000;
const REFETCH_MIN_MS = 60 * 1000;
const SKEW = 60;

interface Claims {
  iss: string;
  aud: string | string[];
  exp: number;
  nbf?: number;
  repository?: string;
  ref?: string;
}

let jwks: { keys: (JsonWebKey & { kid?: string })[]; fetched: number } | null = null;

async function signingKey(kid: string): Promise<JsonWebKey | undefined> {
  const stale = !jwks || Date.now() - jwks.fetched > JWKS_TTL_MS;
  let k = stale ? undefined : jwks!.keys.find((x) => x.kid === kid);
  // GitHub rotates keys; refetch for an unknown kid, but not on every request.
  if (!k && (stale || Date.now() - jwks!.fetched > REFETCH_MIN_MS)) {
    const resp = await fetch(JWKS_URL);
    if (!resp.ok) throw new Error(`fetching GitHub's OIDC keys: ${resp.status}`);
    jwks = { keys: ((await resp.json()) as { keys: (JsonWebKey & { kid?: string })[] }).keys, fetched: Date.now() };
    k = jwks.keys.find((x) => x.kid === kid);
  }
  return k;
}

/** Test hook: forget cached keys. */
export function resetKeys(): void {
  jwks = null;
}

/**
 * Verifies a GitHub Actions OIDC token for this Worker. Returns the
 * identity ("github-actions:owner/repo@ref") or null.
 */
export async function verifyActionsToken(token: string, audience: string, repositories: string[]): Promise<string | null> {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  let header: { alg?: string; kid?: string };
  let claims: Claims;
  try {
    header = JSON.parse(new TextDecoder().decode(fromBase64url(parts[0])));
    claims = JSON.parse(new TextDecoder().decode(fromBase64url(parts[1])));
  } catch {
    return null;
  }
  if (header.alg !== "RS256" || !header.kid) return null;
  const jwk = await signingKey(header.kid);
  if (!jwk) return null;
  const key = await crypto.subtle.importKey("jwk", jwk, { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" }, false, ["verify"]);
  const valid = await crypto.subtle.verify(
    "RSASSA-PKCS1-v1_5",
    key,
    fromBase64url(parts[2]),
    new TextEncoder().encode(`${parts[0]}.${parts[1]}`),
  );
  if (!valid) return null;

  const t = Math.floor(Date.now() / 1000);
  const auds = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
  if (claims.iss !== ISSUER || !auds.includes(audience)) return null;
  if (typeof claims.exp !== "number" || claims.exp < t - SKEW) return null;
  if (typeof claims.nbf === "number" && claims.nbf > t + SKEW) return null;
  const repo = claims.repository?.toLowerCase();
  if (!repo || !repositories.includes(repo)) return null;
  return `github-actions:${claims.repository}@${claims.ref ?? ""}`;
}
