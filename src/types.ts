/** A pointer to a specific, immutable version of an object. */
export interface ObjectRef {
  kind: 'object';
  objectId: string;
  version: number;
}

/** A pointer to another alias, forming a chain. */
export interface AliasRef {
  kind: 'alias';
  alias: string;
}

export type AliasTarget = ObjectRef | AliasRef;

/** What a resolution chain ran into when the next hop does not exist. */
export type MissingTarget =
  | { kind: 'alias'; alias: string }
  | { kind: 'object'; objectId: string; version: number };

export interface ResolveOk {
  ok: true;
  /** Every alias visited, in order, from the requested name to the final alias. */
  path: string[];
  object: ObjectRef;
}

export interface ResolveCycle {
  ok: false;
  reason: 'cycle';
  /** Full walk up to and including the alias that closes the loop. */
  path: string[];
  /** The loop itself: first element repeated at the end. */
  cycle: string[];
}

export interface ResolveDangling {
  ok: false;
  reason: 'dangling';
  path: string[];
  missing: MissingTarget;
}

export interface ResolveMaxHops {
  ok: false;
  reason: 'max-hops';
  /** Aliases visited before the hop budget ran out. */
  path: string[];
}

export type ResolveResult = ResolveOk | ResolveCycle | ResolveDangling | ResolveMaxHops;

/**
 * A read observed during resolution. Reads of entries that turned out to be
 * missing (`existed: false`) are negative dependencies: creating that entry
 * later must invalidate any cached result that depended on its absence.
 */
export type Dependency =
  | { kind: 'alias'; alias: string; existed: boolean }
  | { kind: 'object'; objectId: string; version: number; existed: boolean };

export function dependencyKey(dep: Dependency): string {
  return dep.kind === 'alias'
    ? `alias:${dep.alias}`
    : `object:${dep.objectId}@${dep.version}`;
}

/** Read-only view the resolver traverses. */
export interface StoreView {
  /** Live target of an alias at a snapshot, or null when absent/deleted. */
  getAliasTarget(name: string, snapshot?: number): AliasTarget | null;
  /** Whether the object version exists at a snapshot (defaults to latest). */
  hasObject(objectId: string, version: number, snapshot?: number): boolean;
}

export type AliasOp =
  | { type: 'set'; alias: string; target: AliasTarget }
  | { type: 'delete'; alias: string };

export interface CycleReport {
  alias: string;
  cycle: string[];
  path: string[];
}

export interface DanglingReport {
  alias: string;
  missing: MissingTarget;
  path: string[];
}

export type BatchOutcome =
  | { applied: true; seq: number; cycles: CycleReport[]; dangling: DanglingReport[] }
  | { applied: false; cycles: CycleReport[]; dangling: DanglingReport[] };

/** Notification emitted after a committed mutation, for cache invalidation. */
export interface StoreChange {
  aliases: string[];
  objects: Array<{ objectId: string; version: number }>;
}
