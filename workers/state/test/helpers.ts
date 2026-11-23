import { env } from "cloudflare:test";
import worker from "../src/index";

export const ORIGIN = "https://state.example.com";
export const ADMIN = { Authorization: "Bearer test-admin-token" };

export function call(path: string, init: RequestInit = {}): Promise<Response> {
  return worker.fetch(new Request(ORIGIN + path, { redirect: "manual", ...init }), env);
}

export function lockBody(id: string, who = "josh@laptop", operation = "apply"): string {
  return JSON.stringify({ ID: id, Who: who, Operation: operation, Created: new Date().toISOString() });
}
