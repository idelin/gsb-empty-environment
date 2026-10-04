/** Provenance tree: tracks the source of every final element and its overridden chain. */
import type { SourceRef } from './types.ts';

export interface ProvEntry {
  source: SourceRef;
  /** Raw (unmasked) value contributed by this layer at this node. */
  value: unknown;
}

export interface ProvNode {
  /**
   * entries[0] is the winning contribution (newest);
   * entries[1..] form the overridden chain, newest-first.
   */
  entries: ProvEntry[];
  /** Object keys and array indices (as strings). */
  children: Map<string, ProvNode>;
}

export function makeNode(): ProvNode {
  return { entries: [], children: new Map() };
}

/** Split "a.b[2].c" into ["a", "b", "2", "c"]. */
export function splitPath(path: string): string[] {
  if (path === '') return [];
  return path.replace(/\[(\d+)\]/g, '.$1').split('.');
}

export function nodeAt(root: ProvNode, path: string): ProvNode | undefined {
  let node: ProvNode | undefined = root;
  for (const seg of splitPath(path)) {
    node = node?.children.get(seg);
    if (node === undefined) return undefined;
  }
  return node;
}

export interface ProvenanceInfo {
  source: SourceRef;
  value: unknown;
  /** Overridden contributions, newest-first. Empty when never overridden. */
  overridden: ProvEntry[];
}

/** Provenance for a final element: winning source plus the overridden chain. */
export function provenanceAt(root: ProvNode, path: string): ProvenanceInfo | undefined {
  const node = nodeAt(root, path);
  if (node === undefined || node.entries.length === 0) return undefined;
  const [head, ...rest] = node.entries;
  return { source: head!.source, value: head!.value, overridden: rest };
}

/** Deep structural equality (used by $remove and secret comparison). */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i]));
  }
  if (typeof a === 'object') {
    const aKeys = Object.keys(a as object);
    const bKeys = Object.keys(b as object);
    if (aKeys.length !== bKeys.length) return false;
    return aKeys.every(
      (k) =>
        Object.prototype.hasOwnProperty.call(b, k) &&
        deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
    );
  }
  return false;
}
