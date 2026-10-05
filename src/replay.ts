/**
 * Deterministic operation-log replay.
 *
 * A log is pure data: it can be recorded, printed, and replayed against a
 * fresh store plus a fresh deep-copy ReferenceModel. After every mutating
 * step the runner cross-checks:
 *   1. store head state === reference head state
 *   2. every non-evicted store version deep-equals reference history
 *   3. ledger invariant: retained === allocatedTotal - freedTotal
 *   4. every notification delivers the complete state of its version
 * No timers are involved anywhere: reentrancy is synchronous by design.
 */

import { jsonEqual } from './json.ts';
import type { JsonObject, JsonValue, Path } from './json.ts';
import { ReferenceModel, applyOpMutating, deepCopy } from './reference.ts';
import type { RefOp } from './reference.ts';
import { TransactionalStore } from './store.ts';
import type { Snapshot } from './store.ts';

export type LogEntry =
  | { op: 'begin'; tx: string; base?: number }
  | { op: 'set'; tx: string; path: Path; value: JsonValue }
  | { op: 'delete'; tx: string; path: Path }
  | { op: 'commit'; tx: string }
  | { op: 'snapshot'; id: string; version?: number }
  | { op: 'release'; id: string };

export interface ReplayReport {
  /** Versions the recorder subscriber observed, in delivery order. */
  notifications: number[];
  /** Versions produced by commits, in commit order. */
  commits: number[];
  /** Number of store-vs-reference cross-checks that passed. */
  checks: number;
  /** Non-empty if any subscriber ever observed a state that diverged. */
  inconsistencies: string[];
}

export function replay(
  store: TransactionalStore,
  reference: ReferenceModel,
  log: LogEntry[],
): ReplayReport {
  const pending = new Map<string, { base: number; ops: RefOp[] }>();
  const snapshots = new Map<string, Snapshot>();
  const notifications: number[] = [];
  const commits: number[] = [];
  const inconsistencies: string[] = [];
  let checks = 0;

  store.subscribe((state, version) => {
    notifications.push(version);
    const expected = reference.history.get(version);
    if (expected === undefined || !jsonEqual(state, expected)) {
      inconsistencies.push(`notification for version ${version} carried a divergent state`);
    }
  });

  const crossCheck = (): void => {
    const headRef = reference.history.get(reference.headVersion)!;
    if (!jsonEqual(store.state, headRef)) {
      inconsistencies.push(`head diverged after log step (store version ${store.version})`);
    }
    checks += 1;
    for (const [version, expected] of reference.history) {
      if (store.isEvicted(version)) continue;
      if (!store.hasVersion(version)) {
        inconsistencies.push(`version ${version} missing from store history`);
        continue;
      }
      if (!jsonEqual(store.checkout(version).state, expected)) {
        inconsistencies.push(`version ${version} diverged from reference history`);
      }
      checks += 1;
    }
    const ledger = store.ledger();
    if (ledger.retained !== ledger.allocatedTotal - ledger.freedTotal) {
      inconsistencies.push(
        `ledger invariant broken: ${ledger.retained} != ${ledger.allocatedTotal} - ${ledger.freedTotal}`,
      );
    }
    checks += 1;
  };

  for (const entry of log) {
    switch (entry.op) {
      case 'begin': {
        const base = entry.base ?? store.version;
        pending.set(entry.tx, { base, ops: [] });
        break;
      }
      case 'set':
      case 'delete': {
        const tx = pending.get(entry.tx);
        if (!tx) throw new Error(`log: unknown transaction ${entry.tx}`);
        tx.ops.push(
          entry.op === 'set'
            ? { kind: 'set', path: entry.path, value: entry.value }
            : { kind: 'delete', path: entry.path },
        );
        break;
      }
      case 'commit': {
        const tx = pending.get(entry.tx);
        if (!tx) throw new Error(`log: unknown transaction ${entry.tx}`);
        pending.delete(entry.tx);
        const transaction = store.begin({ base: tx.base });
        for (const op of tx.ops) {
          if (op.kind === 'set') transaction.set(op.path, op.value);
          else transaction.delete(op.path);
        }
        const refVersion = reference.commit(tx.base, tx.ops);
        const storeVersion = transaction.commit();
        if (storeVersion !== refVersion) {
          inconsistencies.push(`version mismatch: store ${storeVersion} vs reference ${refVersion}`);
        }
        commits.push(storeVersion);
        crossCheck();
        break;
      }
      case 'snapshot': {
        snapshots.set(entry.id, store.snapshot(entry.version));
        break;
      }
      case 'release': {
        const snap = snapshots.get(entry.id);
        if (!snap) throw new Error(`log: unknown snapshot ${entry.id}`);
        snap.release();
        snapshots.delete(entry.id);
        crossCheck();
        break;
      }
    }
  }
  return { notifications, commits, checks, inconsistencies };
}

/* ------------------------------------------------------------------ */
/* Deterministic log generator (seeded LCG; no timers, no randomness  */
/* beyond the seed).                                                   */
/* ------------------------------------------------------------------ */

function lcg(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (state * 1664525 + 1013904223) >>> 0;
    return state / 2 ** 32;
  };
}

const KEY_POOL = ['cart', 'inventory', 'ui', 'session', 'a', 'b', 'items', 'meta'];

function randomJsonValue(rand: () => number, depth: number): JsonValue {
  const roll = rand();
  if (depth >= 2 || roll < 0.45) {
    const pick = rand();
    if (pick < 0.25) return null;
    if (pick < 0.5) return Math.floor(rand() * 1000);
    if (pick < 0.75) return `s${Math.floor(rand() * 100)}`;
    return rand() < 0.5;
  }
  if (roll < 0.75) {
    const length = Math.floor(rand() * 3);
    const out: JsonValue[] = [];
    for (let i = 0; i < length; i += 1) out.push(randomJsonValue(rand, depth + 1));
    return out;
  }
  const out: JsonObject = {};
  const count = Math.floor(rand() * 3);
  for (let i = 0; i < count; i += 1) {
    out[KEY_POOL[Math.floor(rand() * KEY_POOL.length)] + Math.floor(rand() * 3)] =
      randomJsonValue(rand, depth + 1);
  }
  return out;
}

/** Collect every existing path (depth-limited) of a JSON tree. */
function collectPaths(value: JsonValue, prefix: Path, depth: number, out: Path[]): void {
  if (depth > 3 || typeof value !== 'object' || value === null) return;
  if (Array.isArray(value)) {
    for (let i = 0; i < value.length; i += 1) {
      const path = [...prefix, i];
      out.push(path);
      collectPaths(value[i], path, depth + 1, out);
    }
  } else {
    for (const key of Object.keys(value)) {
      const path = [...prefix, key];
      out.push(path);
      collectPaths((value as JsonObject)[key], path, depth + 1, out);
    }
  }
}

export interface GenerateOptions {
  steps: number;
  seed: number;
  initial: JsonObject;
}

/**
 * Builds a log that is valid by construction:
 * - transaction bases are always the head or a currently pinned version
 *   (pinned versions are never evicted, so replay can never hit a
 *   reclaimed base);
 * - snapshots pin the head at the moment they are taken;
 * - deletes target paths that exist in the transaction's working state.
 */
export function generateLog(options: GenerateOptions): LogEntry[] {
  const rand = lcg(options.seed);
  const history = new Map<number, JsonObject>();
  history.set(0, deepCopy(options.initial));
  let headVersion = 0;
  let lastVersion = 0;
  const log: LogEntry[] = [];
  const openSnapshots: Array<{ id: string; version: number }> = [];
  let txCounter = 0;
  let snapCounter = 0;

  const pickBase = (): number => {
    if (openSnapshots.length > 0 && rand() < 0.3) {
      return openSnapshots[Math.floor(rand() * openSnapshots.length)].version;
    }
    return headVersion;
  };

  for (let step = 0; step < options.steps; step += 1) {
    const roll = rand();
    if (roll < 0.08 && openSnapshots.length < 4) {
      const id = `snap${snapCounter++}`;
      log.push({ op: 'snapshot', id });
      openSnapshots.push({ id, version: headVersion });
      continue;
    }
    if (roll < 0.16 && openSnapshots.length > 0) {
      const index = Math.floor(rand() * openSnapshots.length);
      const [entry] = openSnapshots.splice(index, 1);
      log.push({ op: 'release', id: entry.id });
      continue;
    }
    const tx = `tx${txCounter++}`;
    const base = pickBase();
    const working = deepCopy(history.get(base)!) as JsonObject;
    const opCount = 1 + Math.floor(rand() * 4);
    log.push({ op: 'begin', tx, base });
    for (let i = 0; i < opCount; i += 1) {
      const paths: Path[] = [];
      collectPaths(working, [], 0, paths);
      if (rand() < 0.25 && paths.length > 0) {
        const path = paths[Math.floor(rand() * paths.length)];
        applyOpMutating(working, { kind: 'delete', path });
        log.push({ op: 'delete', tx, path });
      } else {
        let path: Path;
        if (paths.length > 0 && rand() < 0.7) {
          path = paths[Math.floor(rand() * paths.length)];
        } else {
          const first = KEY_POOL[Math.floor(rand() * KEY_POOL.length)];
          path = [first];
          const existing = (working as JsonObject)[first];
          const canDescend =
            existing === undefined ||
            (typeof existing === 'object' && existing !== null && !Array.isArray(existing));
          if (rand() < 0.4 && canDescend) path = [first, `n${Math.floor(rand() * 4)}`];
        }
        const value = randomJsonValue(rand, 0);
        applyOpMutating(working, { kind: 'set', path, value });
        log.push({ op: 'set', tx, path, value });
      }
    }
    log.push({ op: 'commit', tx });
    const version = ++lastVersion;
    history.set(version, working);
    headVersion = version;
  }
  for (const entry of openSnapshots) log.push({ op: 'release', id: entry.id });
  return log;
}
