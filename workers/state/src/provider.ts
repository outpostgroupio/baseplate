import { github } from "./github";
import type { IdentityProvider } from "./identity";

/**
 * The identity provider users sign in with. To authenticate against
 * something else, implement IdentityProvider (see identity.ts and github.ts)
 * and use it here.
 */
export const identityProvider: IdentityProvider = github;
