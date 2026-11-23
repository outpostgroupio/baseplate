// Sealed values: JSON encrypted and authenticated with AES-GCM under a key
// derived from SESSION_SECRET. The Worker issues three kinds, told apart by
// `typ` so one can't be used as another: the sign-in state it round-trips
// through the identity provider, the short-lived authorization code, and the
// access token. None needs storage, so revoking access is the identity
// provider's job (see IdentityProvider.check).

const encoder = new TextEncoder();
const decoder = new TextDecoder();

const keys = new Map<string, Promise<CryptoKey>>();

function key(secret: string): Promise<CryptoKey> {
  let k = keys.get(secret);
  if (!k) {
    k = crypto.subtle
      .digest("SHA-256", encoder.encode("baseplate-state/seal/v1\0" + secret))
      .then((raw) => crypto.subtle.importKey("raw", raw, "AES-GCM", false, ["encrypt", "decrypt"]));
    keys.set(secret, k);
  }
  return k;
}

export interface Sealed {
  typ: string;
  /** Expiry, in seconds since the epoch. */
  exp: number;
}

export async function seal<T extends Sealed>(secret: string, value: T): Promise<string> {
  if (!secret) throw new Error("SESSION_SECRET is not set");
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv }, await key(secret), encoder.encode(JSON.stringify(value)));
  const out = new Uint8Array(iv.length + ct.byteLength);
  out.set(iv);
  out.set(new Uint8Array(ct), iv.length);
  return base64url(out);
}

/** Opens a sealed value of the given type, or returns null if it is forged, expired or another type. */
export async function unseal<T extends Sealed>(secret: string, token: string, typ: string): Promise<T | null> {
  if (!secret) throw new Error("SESSION_SECRET is not set");
  let bytes: Uint8Array;
  try {
    bytes = fromBase64url(token);
  } catch {
    return null;
  }
  if (bytes.length < 13) return null;
  let value: T;
  try {
    const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv: bytes.slice(0, 12) }, await key(secret), bytes.slice(12));
    value = JSON.parse(decoder.decode(pt));
  } catch {
    return null;
  }
  if (value.typ !== typ || typeof value.exp !== "number" || value.exp < now()) return null;
  return value;
}

export function now(): number {
  return Math.floor(Date.now() / 1000);
}

export function base64url(bytes: Uint8Array | ArrayBuffer): string {
  const b = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  let s = "";
  for (const c of b) s += String.fromCharCode(c);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromBase64url(s: string): Uint8Array {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(b.length);
  for (let i = 0; i < b.length; i++) out[i] = b.charCodeAt(i);
  return out;
}

export async function sha256(s: string): Promise<string> {
  return base64url(await crypto.subtle.digest("SHA-256", encoder.encode(s)));
}

/** Constant-time string comparison. */
export function safeEqual(a: string, b: string): boolean {
  const ab = encoder.encode(a);
  const bb = encoder.encode(b);
  if (ab.length !== bb.length) return false;
  return crypto.subtle.timingSafeEqual(ab, bb);
}
