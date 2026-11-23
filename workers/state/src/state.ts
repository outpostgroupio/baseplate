import { DurableObject } from "cloudflare:workers";
import type { Env } from "./env";

/**
 * A lock as the Terraform/OpenTofu http backend protocol sends it. Only ID is
 * required; the rest is shown to whoever is blocked by the lock.
 */
export interface LockInfo {
  ID: string;
  Operation?: string;
  Info?: string;
  Who?: string;
  Version?: string;
  Created?: string;
  Path?: string;
}

export type Result = { ok: true; lock?: LockInfo } | { ok: false; lock: LockInfo };

/**
 * One StateObject per state name. It holds the state's lock in its own
 * storage, which is strongly consistent, so two runs can't both take the lock,
 * and it checks the lock and writes the state to R2 as one step, so a run that
 * doesn't hold the lock can't write.
 */
export class StateObject extends DurableObject<Env> {
  async getState(name: string): Promise<string | null> {
    const obj = await this.env.STATE_BUCKET.get(stateKey(name));
    return obj ? obj.text() : null;
  }

  /**
   * Writes the whole state in one PUT. When the state is locked, lockID must
   * be the held lock's ID. A copy goes under history/ unless KEEP_HISTORY is
   * "false".
   */
  async putState(name: string, body: string, lockID: string | null): Promise<Result> {
    // Nothing else may run on this object (e.g. an unlock and a new lock)
    // between the lock check and the write.
    return this.ctx.blockConcurrencyWhile(async () => {
      const lock = await this.ctx.storage.get<LockInfo>("lock");
      if (lock && lock.ID !== lockID) return { ok: false, lock };
      const meta = { httpMetadata: { contentType: "application/json" } };
      await this.env.STATE_BUCKET.put(stateKey(name), body, meta);
      if (this.env.KEEP_HISTORY !== "false") {
        await this.env.STATE_BUCKET.put(`history/${name}/${new Date().toISOString()}.bpstate`, body, meta);
      }
      return { ok: true };
    });
  }

  /** Takes the lock, or reports the lock that is already held. */
  async lock(info: LockInfo): Promise<Result> {
    const held = await this.ctx.storage.get<LockInfo>("lock");
    if (held) return { ok: false, lock: held };
    await this.ctx.storage.put("lock", info);
    return { ok: true, lock: info };
  }

  /**
   * Releases the lock with the given ID. Releasing when nothing is locked
   * succeeds; a different ID fails and reports the held lock.
   */
  async unlock(id: string): Promise<Result> {
    const held = await this.ctx.storage.get<LockInfo>("lock");
    if (!held) return { ok: true };
    if (held.ID !== id) return { ok: false, lock: held };
    await this.ctx.storage.delete("lock");
    return { ok: true };
  }
}

export function stateKey(name: string): string {
  return `states/${name}.bpstate`;
}

/**
 * A state name is one or more path segments of letters, digits, ".", "_" and
 * "-", e.g. "openfront/staging". "." and ".." segments are rejected.
 */
export function validName(name: string): boolean {
  if (name.length === 0 || name.length > 200) return false;
  return name.split("/").every((seg) => /^[A-Za-z0-9._-]+$/.test(seg) && seg !== "." && seg !== "..");
}
