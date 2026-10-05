// Generic incremental attribute evaluator.
//
// Every attribute instance is a slot keyed by a string such as
// `type@n12` or `cell@n4:x` (node id + attribute name, or block id +
// variable name for scope cells; never a bare variable name). Slots are
// computed on demand, memoized, and every read of one slot from inside
// another slot's computation is recorded as an explicit dependency edge
// in `depLog`.
//
// Incrementality:
//   * `invalidate(seeds)` marks slots dirty and propagates dirtiness
//     along reverse dependency edges.
//   * A dirty slot is first *verified*: its dependencies are refreshed
//     and if none of their versions changed, the cached value is kept
//     without recomputing (early cutoff).
//   * When a slot does recompute, its version only bumps if the value
//     actually changed (deep equality), which stops change propagation
//     for values that are position-insensitive.
//
// Cyclic attribute dependencies are detected with the evaluation stack
// and reported as a CycleError carrying the concrete attribute path.

export type AttrKey = string;
export type ComputeFn = () => unknown;

export class CycleError extends Error {
  readonly path: AttrKey[];
  constructor(path: AttrKey[]) {
    super(`cyclic attribute dependency: ${path.join(' -> ')}`);
    this.name = 'CycleError';
    this.path = path;
  }
}

export interface DepEdge {
  seq: number;
  observer: AttrKey;
  dependency: AttrKey;
}

interface Slot {
  key: AttrKey;
  compute: ComputeFn;
  state: 'clean' | 'dirty' | 'computing';
  value: unknown;
  hasValue: boolean;
  version: number;
  deps: Set<AttrKey>;
  rdeps: Set<AttrKey>;
  depVersions: Map<AttrKey, number>;
}

export function deepEqual(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    return (
      Array.isArray(a) &&
      Array.isArray(b) &&
      a.length === b.length &&
      a.every((x, i) => deepEqual(x, (b as unknown[])[i]))
    );
  }
  const ka = Object.keys(a as object);
  const kb = Object.keys(b as object);
  if (ka.length !== kb.length) return false;
  return ka.every((k) =>
    deepEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]),
  );
}

export class Evaluator {
  private readonly slots = new Map<AttrKey, Slot>();
  private readonly stack: AttrKey[] = [];
  private readonly factory: (key: AttrKey) => ComputeFn;
  private readonly structural: (key: AttrKey) => boolean;
  readonly depLog: DepEdge[] = [];
  private seq = 0;
  readonly stats = { invalidated: 0, recomputed: 0, verified: 0 };

  // `structural` marks slots whose compute function reads mutable AST
  // structure directly (statement lists, parent links, names). Their
  // recorded dependency set can itself be stale after an edit, so a
  // dirty structural slot is always recomputed; only pure
  // attribute-to-attribute slots are eligible for verification.
  constructor(factory: (key: AttrKey) => ComputeFn, structural: (key: AttrKey) => boolean = () => false) {
    this.factory = factory;
    this.structural = structural;
  }

  resetStats(): void {
    this.stats.invalidated = 0;
    this.stats.recomputed = 0;
    this.stats.verified = 0;
  }

  keys(): AttrKey[] {
    return [...this.slots.keys()];
  }

  has(key: AttrKey): boolean {
    return this.slots.has(key);
  }

  versionOf(key: AttrKey): number | undefined {
    return this.slots.get(key)?.version;
  }

  depsOf(key: AttrKey): AttrKey[] {
    return [...(this.slots.get(key)?.deps ?? [])];
  }

  rdepsOf(key: AttrKey): AttrKey[] {
    return [...(this.slots.get(key)?.rdeps ?? [])];
  }

  get(key: AttrKey): unknown {
    const slot = this.ensure(key);
    const observer = this.stack[this.stack.length - 1];
    if (observer !== undefined && observer !== key) {
      const obs = this.slots.get(observer);
      if (obs !== undefined && !obs.deps.has(key)) {
        obs.deps.add(key);
        slot.rdeps.add(observer);
        this.seq += 1;
        this.depLog.push({ seq: this.seq, observer, dependency: key });
      }
    }
    return this.refresh(slot);
  }

  private ensure(key: AttrKey): Slot {
    let s = this.slots.get(key);
    if (s === undefined) {
      s = {
        key,
        compute: this.factory(key),
        state: 'dirty',
        value: undefined,
        hasValue: false,
        version: 0,
        deps: new Set(),
        rdeps: new Set(),
        depVersions: new Map(),
      };
      this.slots.set(key, s);
    }
    return s;
  }

  private refresh(slot: Slot): unknown {
    if (slot.state === 'computing') {
      throw new CycleError([...this.stack, slot.key]);
    }
    if (slot.state === 'clean') return slot.value;
    let mustRecompute = !slot.hasValue || this.structural(slot.key);
    if (!mustRecompute) {
      for (const [dep, ver] of slot.depVersions) {
        const d = this.slots.get(dep);
        if (d === undefined) {
          mustRecompute = true;
          break;
        }
        this.refresh(d);
        if (d.version !== ver) {
          mustRecompute = true;
          break;
        }
      }
    }
    if (mustRecompute) {
      this.recompute(slot);
    } else {
      slot.state = 'clean';
      this.stats.verified += 1;
    }
    return slot.value;
  }

  private recompute(slot: Slot): void {
    for (const d of slot.deps) this.slots.get(d)?.rdeps.delete(slot.key);
    slot.deps.clear();
    this.stack.push(slot.key);
    slot.state = 'computing';
    let value: unknown;
    try {
      value = slot.compute();
    } finally {
      this.stack.pop();
      slot.state = 'clean';
    }
    if (!slot.hasValue || !deepEqual(value, slot.value)) slot.version += 1;
    slot.value = value;
    slot.hasValue = true;
    slot.depVersions = new Map();
    for (const d of slot.deps) slot.depVersions.set(d, this.slots.get(d)?.version ?? 0);
    this.stats.recomputed += 1;
  }

  invalidate(keys: Iterable<AttrKey>): void {
    const queue = [...keys];
    while (queue.length > 0) {
      const key = queue.pop() as AttrKey;
      const slot = this.slots.get(key);
      if (slot === undefined || slot.state === 'dirty') continue;
      slot.state = 'dirty';
      this.stats.invalidated += 1;
      for (const r of slot.rdeps) queue.push(r);
    }
  }

  removeWhere(pred: (key: AttrKey) => boolean): void {
    for (const key of [...this.slots.keys()]) {
      if (!pred(key)) continue;
      const slot = this.slots.get(key) as Slot;
      for (const d of slot.deps) this.slots.get(d)?.rdeps.delete(key);
      for (const r of slot.rdeps) this.slots.get(r)?.deps.delete(key);
      this.slots.delete(key);
    }
  }

  // Indented dump of the current dependency graph below `root`.
  explain(root: AttrKey, maxDepth = 8): string {
    const lines: string[] = [];
    const seen = new Set<AttrKey>();
    const visit = (key: AttrKey, depth: number): void => {
      lines.push(`${'  '.repeat(depth)}${key}`);
      if (depth >= maxDepth || seen.has(key)) return;
      seen.add(key);
      for (const d of this.depsOf(key)) visit(d, depth + 1);
    };
    visit(root, 0);
    return lines.join('\n');
  }

  // Concrete dependency path between two attribute instances, if any.
  depPath(from: AttrKey, to: AttrKey): AttrKey[] | null {
    if (from === to) return [from];
    const prev = new Map<AttrKey, AttrKey>();
    const seen = new Set<AttrKey>([from]);
    const queue: AttrKey[] = [from];
    while (queue.length > 0) {
      const cur = queue.shift() as AttrKey;
      for (const d of this.depsOf(cur)) {
        if (seen.has(d)) continue;
        seen.add(d);
        prev.set(d, cur);
        if (d === to) {
          const path = [to];
          let p = to;
          while (p !== from) {
            p = prev.get(p) as AttrKey;
            path.unshift(p);
          }
          return path;
        }
        queue.push(d);
      }
    }
    return null;
  }
}
