import type { Env } from "./env";

/**
 * The signed-in user, as stored (encrypted) in the token `baseplate login`
 * receives. `subject` names the user in lock errors; `data` is whatever the
 * provider needs to check the session later (e.g. an upstream access token).
 */
export interface Session {
  subject: string;
  data: Record<string, string>;
}

/** Thrown by an IdentityProvider when sign-in fails; the message is shown to the user. */
export class SignInError extends Error {}

/**
 * Where users sign in. This is the one piece to replace in a fork that
 * authenticates against something other than GitHub: implement it and point
 * `identityProvider` in provider.ts at the implementation. Everything else
 * (the login protocol the CLI speaks, tokens, locking, storage) stays as is.
 */
export interface IdentityProvider {
  /** Whether sign-in is configured. When false, login.v1 isn't advertised. */
  enabled(env: Env): boolean;

  /**
   * The URL to send the browser to. The provider must send the browser back
   * to `callback` with `state` unchanged in the query string.
   */
  authorizeUrl(env: Env, callback: string, state: string): string;

  /**
   * Completes sign-in on the request to `callback` (e.g. exchanges a code for
   * an upstream token) and returns the session. Throws SignInError when the
   * user isn't allowed in.
   */
  complete(env: Env, request: Request, callback: string): Promise<Session>;

  /**
   * Checks, on every state request, that the session is still allowed (e.g.
   * the user is still in the org). Return false to reject the token.
   * Implementations should cache the answer briefly.
   */
  check(env: Env, session: Session): Promise<boolean>;
}
