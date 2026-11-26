import { createHash } from "node:crypto";

/**
 * Deterministic JSON used for signing and certificate digests.
 *
 * Objects are key-sorted, bigint values become decimal strings, and undefined
 * object members are omitted.  The function deliberately refuses functions,
 * symbols, and cyclic structures so a digest can never silently depend on
 * process-local state.
 */
export function canonicalJson(value: unknown): string {
  const seen = new Set<object>();

  const walk = (node: unknown): unknown => {
    if (node === undefined) return undefined;
    if (node === null) return null;
    if (typeof node === "bigint") return node.toString(10);
    if (typeof node === "number") {
      if (!Number.isFinite(node)) throw new TypeError("canonical JSON cannot encode a non-finite number");
      return node;
    }
    if (typeof node === "string" || typeof node === "boolean") return node;
    if (typeof node === "function" || typeof node === "symbol") {
      throw new TypeError(`canonical JSON cannot encode ${typeof node}`);
    }
    if (node instanceof Uint8Array) {
      return Buffer.from(node).toString("base64");
    }
    if (Array.isArray(node)) {
      if (seen.has(node)) throw new TypeError("canonical JSON cannot encode a cycle");
      seen.add(node);
      const output = node.map((item) => walk(item) ?? null);
      seen.delete(node);
      return output;
    }
    if (typeof node === "object") {
      if (seen.has(node)) throw new TypeError("canonical JSON cannot encode a cycle");
      seen.add(node);
      const object = node as Record<string, unknown>;
      const output: Record<string, unknown> = {};
      for (const key of Object.keys(object).sort()) {
        const item = walk(object[key]);
        if (item !== undefined) output[key] = item;
      }
      seen.delete(node);
      return output;
    }
    throw new TypeError(`canonical JSON cannot encode ${String(node)}`);
  };

  return JSON.stringify(walk(value)) ?? "null";
}

export function sha256Hex(value: string | Uint8Array): string {
  return createHash("sha256").update(value).digest("hex");
}

export function canonicalDigest(value: unknown): string {
  return sha256Hex(canonicalJson(value));
}
