const MAX_LEVEL = 32;
const LEVEL_PROBABILITY = 0.25;

export type SkipKey = readonly [number, string];

interface Level {
  forward: SkipNode | null;
  span: number;
}

class SkipNode {
  key: SkipKey | null;
  backward: SkipNode | null = null;
  levels: Level[];

  constructor(level: number, key: SkipKey | null) {
    this.key = key;
    this.levels = Array.from({ length: level }, () => ({ forward: null, span: 0 }));
  }
}

function compare(a: SkipKey, b: SkipKey): number {
  if (a[0] !== b[0]) return a[0] < b[0] ? -1 : 1;
  if (a[1] === b[1]) return 0;
  return a[1] < b[1] ? -1 : 1;
}

/**
 * Order-statistics skip list (Redis zskiplist style).
 * Keys are (number, string) pairs ordered lexicographically, so duplicate
 * numeric values coexist as distinct entries keyed by their unique id.
 * insert / remove / select-by-rank all run in O(log n) expected time.
 */
export class OrderStatisticSkipList {
  private readonly head = new SkipNode(MAX_LEVEL, null);
  private level = 1;
  private length = 0;

  get size(): number {
    return this.length;
  }

  private randomLevel(): number {
    let lvl = 1;
    while (lvl < MAX_LEVEL && Math.random() < LEVEL_PROBABILITY) lvl++;
    return lvl;
  }

  insert(key: SkipKey): void {
    const update: SkipNode[] = new Array(MAX_LEVEL);
    const rank: number[] = new Array(MAX_LEVEL).fill(0);
    let x: SkipNode = this.head;
    for (let i = this.level - 1; i >= 0; i--) {
      rank[i] = i === this.level - 1 ? 0 : rank[i + 1];
      while (x.levels[i].forward !== null && compare(x.levels[i].forward!.key!, key) < 0) {
        rank[i] += x.levels[i].span;
        x = x.levels[i].forward!;
      }
      update[i] = x;
    }
    const lvl = this.randomLevel();
    if (lvl > this.level) {
      for (let i = this.level; i < lvl; i++) {
        rank[i] = 0;
        update[i] = this.head;
        update[i].levels[i].span = this.length;
      }
      this.level = lvl;
    }
    x = new SkipNode(lvl, key);
    for (let i = 0; i < lvl; i++) {
      x.levels[i].forward = update[i].levels[i].forward;
      update[i].levels[i].forward = x;
      x.levels[i].span = update[i].levels[i].span - (rank[0] - rank[i]);
      update[i].levels[i].span = rank[0] - rank[i] + 1;
    }
    for (let i = lvl; i < this.level; i++) {
      update[i].levels[i].span++;
    }
    x.backward = update[0] === this.head ? null : update[0];
    if (x.levels[0].forward !== null) {
      x.levels[0].forward.backward = x;
    }
    this.length++;
  }

  remove(key: SkipKey): boolean {
    const update: SkipNode[] = new Array(MAX_LEVEL);
    let x: SkipNode = this.head;
    for (let i = this.level - 1; i >= 0; i--) {
      while (x.levels[i].forward !== null && compare(x.levels[i].forward!.key!, key) < 0) {
        x = x.levels[i].forward!;
      }
      update[i] = x;
    }
    const target = x.levels[0].forward;
    if (target === null || compare(target.key!, key) !== 0) return false;
    for (let i = 0; i < this.level; i++) {
      if (update[i].levels[i].forward === target) {
        update[i].levels[i].span += target.levels[i].span - 1;
        update[i].levels[i].forward = target.levels[i].forward;
      } else {
        update[i].levels[i].span--;
      }
    }
    if (target.levels[0].forward !== null) {
      target.levels[0].forward.backward = target.backward;
    }
    while (this.level > 1 && this.head.levels[this.level - 1].forward === null) {
      this.level--;
    }
    this.length--;
    return true;
  }

  /** Returns the key at 1-based rank, or null when out of range. */
  select(rank: number): SkipKey | null {
    if (rank < 1 || rank > this.length) return null;
    let traversed = 0;
    let x: SkipNode = this.head;
    for (let i = this.level - 1; i >= 0; i--) {
      while (x.levels[i].forward !== null && traversed + x.levels[i].span <= rank) {
        traversed += x.levels[i].span;
        x = x.levels[i].forward!;
      }
    }
    return x.key;
  }

  first(): SkipKey | null {
    const node = this.head.levels[0].forward;
    return node === null ? null : node.key;
  }

  toArray(): SkipKey[] {
    const out: SkipKey[] = [];
    let node = this.head.levels[0].forward;
    while (node !== null) {
      out.push(node.key!);
      node = node.levels[0].forward;
    }
    return out;
  }
}
