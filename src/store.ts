import { DEFAULT_MAX_HOPS, traverse } from './resolver';
import {
  AliasOp,
  AliasTarget,
  BatchOutcome,
  CycleReport,
  DanglingReport,
  StoreChange,
  StoreView,
} from './types';

interface AliasRevision {
  /** null marks a tombstone: the alias was deleted at this sequence. */
  target: AliasTarget | null;
  seq: number;
}

interface ObjectVersion {
  data: unknown;
  /** Sequence at which this version was created; older snapshots cannot see it. */
  seq: number;
}

export interface BatchOptions {
  maxHops?: number;
  /**
   * When false (default) a batch that would leave a touched chain dangling is
   * rejected. When true, dangling outcomes are reported but still applied.
   * Cycles are always rejected.
   */
  allowDangling?: boolean;
}

/**
 * Overlay view: the committed state plus a pending batch, used to validate
 * the batch exactly as it would look once applied, without mutating anything.
 */
class OverlayView implements StoreView {
  constructor(
    private readonly base: Store,
    private readonly overlay: ReadonlyMap<string, AliasTarget | null>,
  ) {}

  getAliasTarget(name: string): AliasTarget | null {
    if (this.overlay.has(name)) return this.overlay.get(name) ?? null;
    return this.base.getAliasTarget(name);
  }

  hasObject(objectId: string, version: number): boolean {
    return this.base.hasObject(objectId, version);
  }
}

/**
 * Versioned object store plus snapshot-aware alias store.
 *
 * Objects are immutable per (objectId, version); aliases keep their full
 * revision history so any past sequence number remains resolvable forever.
 * Every committed batch bumps the sequence by exactly one, which makes a
 * batch the atomic unit of both mutation and snapshotting.
 */
export class Store implements StoreView {
  private seq = 0;
  private readonly aliases = new Map<string, AliasRevision[]>();
  private readonly objects = new Map<string, Map<number, ObjectVersion>>();
  private readonly listeners = new Set<(change: StoreChange) => void>();

  get currentSeq(): number {
    return this.seq;
  }

  onChange(listener: (change: StoreChange) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(change: StoreChange): void {
    for (const listener of this.listeners) listener(change);
  }

  /**
   * Create a new immutable object version. Re-creation of a version is
   * rejected. Each creation advances the sequence so that snapshots taken
   * before it never observe the new version.
   */
  putObject(objectId: string, version: number, data: unknown): void {
    let versions = this.objects.get(objectId);
    if (!versions) {
      versions = new Map();
      this.objects.set(objectId, versions);
    }
    if (versions.has(version)) {
      throw new Error(`object ${objectId}@${version} already exists`);
    }
    this.seq++;
    versions.set(version, { data, seq: this.seq });
    this.emit({ aliases: [], objects: [{ objectId, version }] });
  }

  getObject(objectId: string, version: number): unknown {
    return this.objects.get(objectId)?.get(version)?.data;
  }

  hasObject(objectId: string, version: number, snapshot?: number): boolean {
    const found = this.objects.get(objectId)?.get(version);
    if (!found) return false;
    return found.seq <= (snapshot ?? this.seq);
  }

  getAliasTarget(name: string, snapshot?: number): AliasTarget | null {
    const revisions = this.aliases.get(name);
    if (!revisions) return null;
    const at = snapshot ?? this.seq;
    // Binary search for the latest revision with seq <= at.
    let lo = 0;
    let hi = revisions.length - 1;
    let found = -1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const rev = revisions[mid]!;
      if (rev.seq <= at) {
        found = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    return found < 0 ? null : revisions[found]!.target;
  }

  /** Names of all aliases live at a snapshot (defaults to latest). */
  listAliases(snapshot?: number): string[] {
    const names: string[] = [];
    for (const name of this.aliases.keys()) {
      if (this.getAliasTarget(name, snapshot) !== null) names.push(name);
    }
    return names.sort();
  }

  /**
   * Validate and apply a batch atomically.
   *
   * The batch is validated against an overlay of the committed state. Only
   * aliases whose resolution could change (the touched aliases plus their
   * transitive reverse dependents) are re-resolved. Cycles and dangling
   * targets are collected into separate reports; if the batch is rejected,
   * no revision is written and the sequence does not advance.
   *
   * Duplicate ops on the same alias collapse to the last one.
   */
  applyBatch(ops: AliasOp[], opts: BatchOptions = {}): BatchOutcome {
    const overlay = new Map<string, AliasTarget | null>();
    for (const op of ops) {
      overlay.set(op.alias, op.type === 'delete' ? null : op.target);
    }
    if (overlay.size === 0) {
      return { applied: true, seq: this.seq, cycles: [], dangling: [] };
    }

    const view = new OverlayView(this, overlay);
    const affected = this.affectedAliases(view, overlay);
    const maxHops = opts.maxHops ?? DEFAULT_MAX_HOPS;

    const cycles: CycleReport[] = [];
    const dangling: DanglingReport[] = [];
    const seenCycles = new Set<string>();
    for (const name of affected) {
      // Aliases deleted by this batch are expected to dangle; what matters is
      // whether the deletion dangles the aliases that point at them.
      if (overlay.get(name) === null) continue;
      const result = traverse(view, name, { maxHops });
      if (result.ok) continue;
      if (result.reason === 'cycle') {
        const key = canonicalCycleKey(result.cycle);
        if (!seenCycles.has(key)) {
          seenCycles.add(key);
          cycles.push({ alias: name, cycle: result.cycle, path: result.path });
        }
      } else if (result.reason === 'dangling') {
        dangling.push({ alias: name, missing: result.missing, path: result.path });
      }
    }

    if (cycles.length > 0 || (!opts.allowDangling && dangling.length > 0)) {
      return { applied: false, cycles, dangling };
    }

    this.seq++;
    for (const [name, target] of overlay) {
      let revisions = this.aliases.get(name);
      if (!revisions) {
        revisions = [];
        this.aliases.set(name, revisions);
      }
      revisions.push({ target, seq: this.seq });
    }
    this.emit({ aliases: [...overlay.keys()], objects: [] });
    return { applied: true, seq: this.seq, cycles: [], dangling };
  }

  /**
   * Aliases whose resolution may change under the overlay: the touched
   * aliases plus everything that transitively points at them.
   */
  private affectedAliases(
    view: StoreView,
    overlay: ReadonlyMap<string, AliasTarget | null>,
  ): Set<string> {
    const dependents = new Map<string, string[]>();
    const candidates = new Set<string>([...this.aliases.keys(), ...overlay.keys()]);
    for (const name of candidates) {
      const target = view.getAliasTarget(name);
      if (target && target.kind === 'alias') {
        const list = dependents.get(target.alias) ?? [];
        list.push(name);
        dependents.set(target.alias, list);
      }
    }
    const affected = new Set<string>();
    const queue = [...overlay.keys()];
    while (queue.length > 0) {
      const name = queue.pop()!;
      if (affected.has(name)) continue;
      affected.add(name);
      for (const dependent of dependents.get(name) ?? []) queue.push(dependent);
    }
    return affected;
  }
}

/** Rotation-independent key so a loop is reported once regardless of entry point. */
function canonicalCycleKey(cycle: string[]): string {
  const nodes = cycle.slice(0, -1);
  let best: string[] = nodes;
  for (let i = 1; i < nodes.length; i++) {
    const rotated = [...nodes.slice(i), ...nodes.slice(0, i)];
    if (rotated.join('') < best.join('')) best = rotated;
  }
  return best.join('');
}
