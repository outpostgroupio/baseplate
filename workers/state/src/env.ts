import type { StateObject } from "./state";

export interface Env {
  STATE_BUCKET: R2Bucket;
  STATE_OBJECTS: DurableObjectNamespace<StateObject>;

  /** Seals the tokens and codes the Worker issues. Required. */
  SESSION_SECRET: string;
  /** Optional emergency token with full access (e.g. to force-unlock when sign-in is down). */
  ADMIN_TOKEN?: string;

  GITHUB_CLIENT_ID?: string;
  GITHUB_CLIENT_SECRET?: string;
  /** The org whose members may sign in. Sign-in is disabled without it. */
  GITHUB_ORG?: string;
  /** Optional team slug in GITHUB_ORG that members must also belong to. */
  GITHUB_TEAM?: string;
  /** GitHub Enterprise Server: the web URL (default https://github.com) and API URL (default https://api.github.com). */
  GITHUB_URL?: string;
  GITHUB_API_URL?: string;

  /** Comma-separated owner/repo list allowed to use GitHub Actions OIDC tokens. */
  OIDC_REPOSITORIES?: string;
  /**
   * Comma-separated owner/repo list whose GitHub Actions OIDC tokens may only
   * read states' outputs (GET <state>?outputs), for baseplate_remote_state.
   */
  OIDC_OUTPUT_REPOSITORIES?: string;

  TOKEN_TTL_DAYS?: string;
  KEEP_HISTORY?: string;
}
