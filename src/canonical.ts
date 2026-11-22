import { createHash, randomBytes } from "node:crypto";

export function canonicalJson(value: unknown): string {
  return canonicalize(value);
}

function canonicalize(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "string") return JSON.stringify(value);
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isFinite(value)) throw new TypeError("Cannot canonicalize a non-finite number");
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return "[" + value.map(canonicalize).join(",") + "]";
  }
  if (typeof value === "object") {
    const record = value as Record<string, unknown>;
    const keys = Object.keys(record).sort();
    const entries: string[] = [];
    for (const key of keys) {
      const item = record[key];
      if (item === undefined) continue;
      entries.push(JSON.stringify(key) + ":" + canonicalize(item));
    }
    return "{" + entries.join(",") + "}";
  }
  throw new TypeError(`Cannot canonicalize value of type ${typeof value}`);
}

export function sha256(value: string | Uint8Array): string {
  return createHash("sha256").update(value).digest("base64url");
}

export function hashDomain(domain: string, value: unknown): string {
  return sha256(domain + "\n" + canonicalJson(value));
}

export function randomId(prefix: string, bytes = 18): string {
  return `${prefix}_${randomBytes(bytes).toString("base64url")}`;
}

export function randomSecret(bytes = 32): string {
  return randomBytes(bytes).toString("base64url");
}

export function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child);
    }
  }
  return value;
}

export function cloneFrozen<T>(value: T): T {
  return deepFreeze(structuredClone(value));
}

export function deriveUserCode(accountId: string, email: string, purpose: string): string {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const digest = createHash("sha256")
    .update(`user-code.v1\n${accountId}\n${email.toLowerCase()}\n${purpose}`)
    .digest();
  let code = "";
  for (let i = 0; i < 8; i += 1) code += alphabet[digest[i]! % alphabet.length];
  return code;
}

export function constantTimeStringEqual(left: string, right: string): boolean {
  if (left.length !== right.length) return false;
  let diff = 0;
  for (let i = 0; i < left.length; i += 1) {
    diff |= left.charCodeAt(i) ^ right.charCodeAt(i);
  }
  return diff === 0;
}
