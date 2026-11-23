import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { resetKeys } from "../src/oidc";
import { base64url } from "../src/seal";
import { call, ORIGIN } from "./helpers";

let keys: CryptoKeyPair;
let jwk: JsonWebKey;

beforeAll(async () => {
  keys = (await crypto.subtle.generateKey(
    { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true,
    ["sign", "verify"],
  )) as CryptoKeyPair;
  jwk = (await crypto.subtle.exportKey("jwk", keys.publicKey)) as JsonWebKey;
});

afterEach(() => {
  vi.restoreAllMocks();
  resetKeys();
});

function serveKeys() {
  vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    if (url === "https://token.actions.githubusercontent.com/.well-known/jwks") {
      return Response.json({ keys: [{ ...jwk, kid: "k1", alg: "RS256", use: "sig" }] });
    }
    return new Response("unexpected", { status: 500 });
  });
}

async function jwt(claims: Record<string, unknown>, kid = "k1", key = keys.privateKey): Promise<string> {
  const enc = (o: unknown) => base64url(new TextEncoder().encode(JSON.stringify(o)));
  const input = `${enc({ alg: "RS256", typ: "JWT", kid })}.${enc(claims)}`;
  const sig = await crypto.subtle.sign("RSASSA-PKCS1-v1_5", key, new TextEncoder().encode(input));
  return `${input}.${base64url(sig)}`;
}

function claims(over: Record<string, unknown> = {}) {
  const t = Math.floor(Date.now() / 1000);
  return {
    iss: "https://token.actions.githubusercontent.com",
    aud: ORIGIN,
    exp: t + 300,
    nbf: t - 10,
    repository: "acme/game",
    ref: "refs/heads/main",
    ...over,
  };
}

async function status(token: string): Promise<number> {
  return (await call("/states/ci", { headers: { Authorization: `Bearer ${token}` } })).status;
}

describe("GitHub Actions OIDC", () => {
  it("accepts a token from an allowed repository", async () => {
    serveKeys();
    expect(await status(await jwt(claims()))).toBe(204);
  });

  it("rejects the wrong audience, repository, issuer or an expired token", async () => {
    serveKeys();
    const t = Math.floor(Date.now() / 1000);
    expect(await status(await jwt(claims({ aud: "https://other.example.com" })))).toBe(401);
    expect(await status(await jwt(claims({ repository: "acme/other" })))).toBe(401);
    expect(await status(await jwt(claims({ iss: "https://evil.example.com" })))).toBe(401);
    expect(await status(await jwt(claims({ exp: t - 3600 })))).toBe(401);
  });

  it("rejects a token signed with another key", async () => {
    serveKeys();
    const other = (await crypto.subtle.generateKey(
      { name: "RSASSA-PKCS1-v1_5", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
      true,
      ["sign", "verify"],
    )) as CryptoKeyPair;
    expect(await status(await jwt(claims(), "k1", other.privateKey))).toBe(401);
  });
});
