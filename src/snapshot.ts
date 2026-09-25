import { digest } from './canonical.ts';

export type AttrValue = string | number | boolean;

export type SnapshotEntry =
  | { kind: 'value'; value: AttrValue }
  | { kind: 'sealed'; hash: string };

/**
 * Immutable attribute snapshot captured at evaluation time.
 * A sealed entry keeps only a hash placeholder of the sensitive value.
 */
export type Snapshot = Record<string, SnapshotEntry>;

export function fromAttributes(attrs: Record<string, AttrValue>): Snapshot {
  const snapshot: Snapshot = {};
  for (const [key, value] of Object.entries(attrs)) {
    snapshot[key] = { kind: 'value', value };
  }
  return snapshot;
}

export function sealValue(value: AttrValue): string {
  return digest({ sealed: value });
}

/** Seal a single attribute independently; other fields stay in the clear. */
export function sealAttribute(snapshot: Snapshot, attr: string): Snapshot {
  const entry = snapshot[attr];
  if (!entry) {
    throw new Error(`cannot seal missing attribute "${attr}"`);
  }
  const next = cloneSnapshot(snapshot);
  next[attr] =
    entry.kind === 'sealed'
      ? entry
      : { kind: 'sealed', hash: sealValue(entry.value) };
  return next;
}

export function cloneSnapshot(snapshot: Snapshot): Snapshot {
  return structuredClone(snapshot);
}
