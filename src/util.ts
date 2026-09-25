import type { JsonValue, TreeNode } from './types.ts';

export function childrenOf(node: TreeNode): TreeNode[] {
  return node.children ?? [];
}

export function attrsOf(node: TreeNode): { [key: string]: JsonValue } {
  return node.attrs ?? {};
}

/** Deep clone through JSON; inputs are validated JSON-compatible values. */
export function deepClone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

/** Order-insensitive (for objects) deep equality of JSON values. */
export function jsonEquals(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a)) {
    return (
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((item, i) => jsonEquals(item, (b as JsonValue[])[i]))
    );
  }
  if (a !== null && typeof a === 'object' && b !== null && typeof b === 'object' && !Array.isArray(b)) {
    const recA = a as { [key: string]: JsonValue };
    const recB = b as { [key: string]: JsonValue };
    const keysA = Object.keys(recA);
    const keysB = Object.keys(recB);
    return (
      keysA.length === keysB.length &&
      keysA.every((key) => key in recB && jsonEquals(recA[key], recB[key]))
    );
  }
  return false;
}

function canonicalJson(value: JsonValue): JsonValue {
  if (Array.isArray(value)) return value.map(canonicalJson);
  if (value !== null && typeof value === 'object') {
    const rec = value as { [key: string]: JsonValue };
    const out: { [key: string]: JsonValue } = {};
    for (const key of Object.keys(rec).sort()) out[key] = canonicalJson(rec[key]);
    return out;
  }
  return value;
}

/**
 * Canonical form used for equality checks: attribute keys sorted, empty
 * `attrs`/`children` dropped. Makes comparison immune to JSON key order.
 */
export function canonicalize(node: TreeNode | null): TreeNode | null {
  if (node === null) return null;
  const out: TreeNode = { id: node.id };
  const attrKeys = Object.keys(attrsOf(node)).sort();
  if (attrKeys.length > 0) {
    const attrs: { [key: string]: JsonValue } = {};
    for (const key of attrKeys) attrs[key] = canonicalJson(attrsOf(node)[key]);
    out.attrs = attrs;
  }
  const kids = childrenOf(node).map((child) => canonicalize(child) as TreeNode);
  if (kids.length > 0) out.children = kids;
  return out;
}

/** Deterministic, locale-independent string comparison. */
export function compareStrings(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}
