import { env } from "cloudflare:test";
import { describe, expect, it } from "vitest";
import { ADMIN, call, lockBody } from "./helpers";

describe("state protocol", () => {
  it("rejects requests without credentials", async () => {
    expect((await call("/states/a")).status).toBe(401);
    expect((await call("/states/a", { headers: { Authorization: "Bearer nope" } })).status).toBe(401);
  });

  it("round-trips state under a lock, as the http backend uses it", async () => {
    expect((await call("/states/rt", { headers: ADMIN })).status).toBe(204);

    const lock = await call("/states/rt", { method: "LOCK", headers: ADMIN, body: lockBody("L1") });
    expect(lock.status).toBe(200);

    const state = JSON.stringify({ version: 2, serial: 1, lineage: "x", resources: [] });
    expect((await call("/states/rt", { method: "POST", headers: ADMIN, body: state })).status).toBe(409);
    expect((await call("/states/rt?ID=L1", { method: "POST", headers: ADMIN, body: state })).status).toBe(200);

    const got = await call("/states/rt", { headers: ADMIN });
    expect(got.status).toBe(200);
    expect(await got.text()).toBe(state);

    expect((await call("/states/rt", { method: "UNLOCK", headers: ADMIN, body: lockBody("L1") })).status).toBe(200);
    const history = await env.STATE_BUCKET.list({ prefix: "history/rt/" });
    expect(history.objects).toHaveLength(1);
  });

  it("refuses a second lock and names the holder", async () => {
    await call("/states/held", { method: "LOCK", headers: ADMIN, body: lockBody("first", "ci@runner", "apply") });
    const second = await call("/states/held", { method: "LOCK", headers: ADMIN, body: lockBody("second") });
    expect(second.status).toBe(423);
    const held = (await second.json()) as { ID: string; Who: string; Operation: string };
    expect(held.ID).toBe("first");
    expect(held.Who).toBe("ci@runner (admin)");
    expect(held.Operation).toBe("apply");
  });

  it("gives the lock to exactly one of two concurrent runs", async () => {
    const [a, b] = await Promise.all([
      call("/states/race", { method: "LOCK", headers: ADMIN, body: lockBody("A") }),
      call("/states/race", { method: "LOCK", headers: ADMIN, body: lockBody("B") }),
    ]);
    expect([a.status, b.status].sort()).toEqual([200, 423]);
  });

  it("only releases the lock with its ID (force-unlock)", async () => {
    await call("/states/fu", { method: "LOCK", headers: ADMIN, body: lockBody("keep") });
    const wrong = await call("/states/fu", { method: "UNLOCK", headers: ADMIN, body: JSON.stringify({ ID: "other" }) });
    expect(wrong.status).toBe(409);
    expect(((await wrong.json()) as { ID: string }).ID).toBe("keep");
    expect((await call("/states/fu", { method: "UNLOCK", headers: ADMIN, body: JSON.stringify({ ID: "keep" }) })).status).toBe(200);
    expect((await call("/states/fu", { method: "LOCK", headers: ADMIN, body: lockBody("next") })).status).toBe(200);
  });

  it("accepts GitLab-style POST/DELETE on <state>/lock and Basic auth", async () => {
    const basic = { Authorization: "Basic " + btoa("terraform:test-admin-token") };
    expect((await call("/states/gl/lock", { method: "POST", headers: basic, body: lockBody("G") })).status).toBe(200);
    expect((await call("/states/gl/lock", { method: "POST", headers: basic, body: lockBody("H") })).status).toBe(423);
    expect((await call("/states/gl/lock", { method: "DELETE", headers: basic, body: lockBody("G") })).status).toBe(200);
  });

  it("accepts nested names and rejects path tricks", async () => {
    expect((await call("/states/openfront/staging", { headers: ADMIN })).status).toBe(204);
    // "a/../b" never arrives: URL parsing resolves it to "b" first.
    expect((await call("/states/a%2F..%2Fb", { headers: ADMIN })).status).toBe(400);
    expect((await call("/states/", { headers: ADMIN })).status).toBe(400);
  });

  it("rejects state that isn't JSON", async () => {
    expect((await call("/states/bad", { method: "POST", headers: ADMIN, body: "not json" })).status).toBe(400);
  });
});

describe("outputs", () => {
  it("advertises outputs.v1", async () => {
    const d = (await (await call("/.well-known/baseplate.json")).json()) as Record<string, unknown>;
    expect(d["outputs.v1"]).toEqual({});
  });

  it("returns only a state's outputs for GET ?outputs", async () => {
    expect((await call("/states/out?outputs", { headers: ADMIN })).status).toBe(204);
    const outputs = { icon: { value: "42", type: "string" } };
    const state = JSON.stringify({ version: 4, serial: 1, lineage: "x", resources: [{ type: "t", name: "n", id: "1" }], outputs });
    expect((await call("/states/out", { method: "POST", headers: ADMIN, body: state })).status).toBe(200);
    const got = await call("/states/out?outputs", { headers: ADMIN });
    expect(got.status).toBe(200);
    expect(await got.json()).toEqual({ outputs });

    const none = JSON.stringify({ version: 3, serial: 1, lineage: "x", resources: [] });
    expect((await call("/states/out", { method: "POST", headers: ADMIN, body: none })).status).toBe(200);
    expect(await (await call("/states/out?outputs", { headers: ADMIN })).json()).toEqual({ outputs: {} });
  });
});
