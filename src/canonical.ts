import { createHash } from "node:crypto";
import type { AliasMap, FieldBinding, FieldPath } from "./types.ts";

/** Join canonical path segments using the package's stable wire separator. */
export function joinPath(path: FieldPath): string {
  if (path.length === 0) {
    throw new Error("field paths must contain at least one segment");
  }
  return path
    .map((segment) => {
      if (typeof segment !== "string" || segment.length === 0) {
        throw new Error("field path segments must be non-empty strings");
      }
      return encodeURIComponent(segment);
    })
    .join("/");
}

/**
 * Deterministic JSON serialization: object keys are sorted recursively and
 * the output contains no whitespace, so the same logical value always has
 * the same bytes regardless of insertion order.
 */
export function stableStringify(value: unknown): string {
  if (value === null || typeof value !== "object") {
    return JSON.stringify(value);
  }
  if (Array.isArray(value)) {
    return `[${value.map((item) => stableStringify(item)).join(",")}]`;
  }
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record).sort();
  return `{${keys
    .map((key) => `${JSON.stringify(key)}:${stableStringify(record[key])}`)
    .join(",")}}`;
}

/** Stable digest used both for the grant binding and snapshot proofs. */
export function digestBytes(bytes: string): string {
  return createHash("sha256").update(bytes, "utf8").digest("base64url");
}

/**
 * Canonicalize a submitted form.
 *
 * Top-level object keys are the tokens the client used. A token may be a
 * canonical single segment or an alias declared in `aliases`; both resolve
 * to the same canonical path. Resolution happens before hashing so alias
 * variants produce identical digests.
 */
export function canonicalizeForm(
  form: Readonly<Record<string, unknown>>,
  aliases: AliasMap = {},
): { readonly bindings: readonly FieldBinding[]; readonly digest: string } {
  const byCanonical = new Map<string, FieldBinding>();
  for (const token of Object.keys(form)) {
    let canonical: FieldPath;
    if (Object.hasOwn(aliases, token)) {
      canonical = aliases[token];
    } else if (token.includes("/")) {
      canonical = token.split("/").map((segment) => decodeURIComponent(segment));
    } else {
      canonical = [token];
    }
    const canonicalKey = joinPath(canonical);
    const binding: FieldBinding = { path: canonical, value: form[token] };
    if (byCanonical.has(canonicalKey)) {
      throw new Error(`duplicate canonical field path: ${canonicalKey}`);
    }
    byCanonical.set(canonicalKey, binding);
  }
  const bindings = [...byCanonical.values()].sort((left, right) =>
    joinPath(left.path) < joinPath(right.path) ? -1 : 1,
  );
  const canonicalPayload = stableStringify(
    bindings.map((binding) => ({
      path: joinPath(binding.path),
      value: binding.value,
    })),
  );
  return { bindings, digest: digestBytes(canonicalPayload) };
}

/** Convenience builder for an alias table. */
export function defineAliases(
  entries: ReadonlyArray<readonly [string, readonly string[]]>,
): AliasMap {
  const map: Record<string, readonly string[]> = {};
  for (const [alias, path] of entries) {
    if (Object.hasOwn(map, alias)) {
      throw new Error(`duplicate alias: ${alias}`);
    }
    map[alias] = path;
  }
  return map;
}
