/**
 * LSEQ — adaptive variable-base identifier allocation over an unbounded
 * fractional causal tree.
 *
 * Each element of the sequence is addressed by an `Identifier`: a path of
 * `(digit, site)` levels into an unbounded tree. The branching factor (base)
 * doubles with depth (8, 16, 32, ...), so dense inserts at one position only
 * descend a level once the current level's space is exhausted, keeping
 * identifier bit-width growth poly-logarithmic in the number of edits.
 *
 * References: Nédelec et al., "LSEQ: an Adaptive Structure for Sequences in
 * Distributed Collaborative Editing" (DocEng 2013).
 */

export interface Level {
  digit: number;
  site: number;
}

/** A position identifier: a root-to-node path in the causal tree. */
export type Identifier = Level[];

export interface InsertOp {
  id: Identifier;
  value: string;
}

export type Rng = () => number;

/** Deterministic PRNG (mulberry32) so tests and demos are reproducible. */
export function mulberry32(seed: number): Rng {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/** Branching factor at a given tree depth: doubles every level. */
export function baseAt(depth: number): number {
  return 2 ** (depth + 3); // 8, 16, 32, 64, ...
}

/** Total order on identifiers: lexicographic on (digit, site) per level. */
export function compareIdentifiers(a: Identifier, b: Identifier): number {
  const len = Math.min(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i].digit !== b[i].digit) return a[i].digit < b[i].digit ? -1 : 1;
    if (a[i].site !== b[i].site) return a[i].site < b[i].site ? -1 : 1;
  }
  if (a.length === b.length) return 0;
  return a.length < b.length ? -1 : 1;
}

export function identifierKey(id: Identifier): string {
  return id.map((l) => `${l.digit}:${l.site}`).join("/");
}

const BOUNDARY = 10;

type Strategy = "boundary+" | "boundary-";

/**
 * Allocate a fresh identifier strictly between `prev` and `next`
 * (use `null` for the open sequence boundary).
 *
 * At each depth the admissible digit interval is `(lo, hi)` where `lo` is
 * prev's digit (or 0) and `hi` is next's digit (or the level base). If the
 * interval is exhausted we carry prev's level into the prefix and descend,
 * so depth only grows when the current level is genuinely full.
 *
 * The boundary strategy is adaptive to the editing direction: prepends
 * (open lower boundary) hug the upper bound with `boundary-`, appends
 * (open upper boundary) hug the lower bound with `boundary+`, and middle
 * inserts pick randomly. This keeps the remaining space maximal where
 * future dense edits will land, giving poly-logarithmic depth growth
 * instead of the linear blow-up a purely random strategy suffers under
 * repeated front/back insertion.
 */
export function allocate(
  prev: Identifier | null,
  next: Identifier | null,
  site: number,
  rng: Rng = Math.random,
): Identifier {
  const id: Level[] = [];
  let depth = 0;
  for (;;) {
    const base = baseAt(depth);
    const pLevel = prev && depth < prev.length ? prev[depth] : null;
    const nLevel = next && depth < next.length ? next[depth] : null;
    const lo = pLevel ? pLevel.digit : 0;
    const hi = nLevel ? nLevel.digit : base;
    const interval = hi - lo;

    if (interval > 1) {
      const strategy: Strategy = !prev
        ? "boundary-"
        : !next
          ? "boundary+"
          : rng() < 0.5
            ? "boundary+"
            : "boundary-";
      const step = Math.min(BOUNDARY, interval - 1);
      const offset = 1 + Math.floor(rng() * step);
      const digit = strategy === "boundary+" ? lo + offset : hi - offset;
      id.push({ digit, site });
      return id;
    }

    // Level exhausted: keep prev's level (or a virtual 0) and go deeper.
    id.push(pLevel ? { digit: pLevel.digit, site: pLevel.site } : { digit: 0, site });
    depth++;
  }
}

/** One node of the unbounded causal tree; children are kept sorted. */
class CausalNode {
  value: string | null = null;
  readonly children = new Map<string, CausalNode>();
}

function levelKey(level: Level): string {
  return `${level.digit}:${level.site}`;
}

/**
 * Replicated sequence backed by the causal tree. Local edits produce
 * `InsertOp`s that commute: applying them in any order on any replica
 * converges to the same sequence ordered by identifier (causal order).
 */
export class LSEQSequence {
  readonly site: number;
  private readonly rng: Rng;
  private readonly root = new CausalNode();
  private readonly seen = new Set<string>();
  private size = 0;

  constructor(site: number, rng: Rng = Math.random) {
    this.site = site;
    this.rng = rng;
  }

  get length(): number {
    return this.size;
  }

  /** Insert `value` at visible `index`; returns the op to broadcast. */
  insert(index: number, value: string): InsertOp {
    if (index < 0 || index > this.size) {
      throw new RangeError(`index ${index} out of bounds (size ${this.size})`);
    }
    const ids = this.identifiers();
    const prev = index > 0 ? ids[index - 1] : null;
    const next = index < ids.length ? ids[index] : null;
    const op: InsertOp = { id: allocate(prev, next, this.site, this.rng), value };
    this.apply(op);
    return op;
  }

  /** Idempotently apply a (local or remote) insert op. */
  apply(op: InsertOp): boolean {
    const key = identifierKey(op.id);
    if (this.seen.has(key)) return false;
    this.seen.add(key);

    let node = this.root;
    for (const level of op.id) {
      const k = levelKey(level);
      let child = node.children.get(k);
      if (!child) {
        child = new CausalNode();
        node.children.set(k, child);
      }
      node = child;
    }
    node.value = op.value;
    this.size++;
    return true;
  }

  /** Merge a batch of ops (any order) from another replica. */
  merge(ops: Iterable<InsertOp>): number {
    let applied = 0;
    for (const op of ops) if (this.apply(op)) applied++;
    return applied;
  }

  /** In-order traversal: identifiers in strict causal order. */
  identifiers(): Identifier[] {
    const out: Identifier[] = [];
    this.walk(this.root, [], (id) => out.push(id));
    return out;
  }

  /** Visible sequence values in causal order. */
  toArray(): string[] {
    const out: string[] = [];
    this.walk(this.root, [], (_id, value) => out.push(value));
    return out;
  }

  toString(): string {
    return this.toArray().join("");
  }

  /** Depth statistics of the current identifier allocation. */
  stats(): { size: number; maxDepth: number; meanDepth: number } {
    const ids = this.identifiers();
    const maxDepth = ids.reduce((m, id) => Math.max(m, id.length), 0);
    const meanDepth = ids.length
      ? ids.reduce((s, id) => s + id.length, 0) / ids.length
      : 0;
    return { size: ids.length, maxDepth, meanDepth };
  }

  private walk(
    node: CausalNode,
    path: Level[],
    visit: (id: Identifier, value: string) => void,
  ): void {
    if (node.value !== null) visit(path, node.value);
    const entries = [...node.children.entries()].sort(([ka], [kb]) =>
      compareIdentifiers([parseLevel(ka)], [parseLevel(kb)]),
    );
    for (const [key, child] of entries) {
      this.walk(child, [...path, parseLevel(key)], visit);
    }
  }
}

function parseLevel(key: string): Level {
  const sep = key.indexOf(":");
  return {
    digit: Number(key.slice(0, sep)),
    site: Number(key.slice(sep + 1)),
  };
}
