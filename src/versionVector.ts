export interface Dot {
  readonly node: string;
  readonly counter: number;
}

export function dotKey(dot: Dot): string {
  return `${dot.node}:${dot.counter}`;
}

export function compareDots(a: Dot, b: Dot): number {
  if (a.node !== b.node) return a.node < b.node ? -1 : 1;
  return a.counter - b.counter;
}

export class VersionVector {
  private readonly entries: Map<string, number>;

  private constructor(entries: Map<string, number>) {
    this.entries = entries;
  }

  static empty(): VersionVector {
    return new VersionVector(new Map());
  }

  static from(entries: Iterable<readonly [string, number]>): VersionVector {
    return new VersionVector(new Map(entries));
  }

  get(node: string): number {
    return this.entries.get(node) ?? 0;
  }

  set(node: string, counter: number): VersionVector {
    const next = new Map(this.entries);
    next.set(node, counter);
    return new VersionVector(next);
  }

  fold(dot: Dot): VersionVector {
    return this.covers(dot) ? this : this.set(dot.node, dot.counter);
  }

  covers(dot: Dot): boolean {
    return this.get(dot.node) >= dot.counter;
  }

  dominates(other: VersionVector): boolean {
    for (const [node, counter] of other.entries) {
      if (this.get(node) < counter) return false;
    }
    return true;
  }

  concurrentWith(other: VersionVector): boolean {
    return !this.dominates(other) && !other.dominates(this);
  }

  join(other: VersionVector): VersionVector {
    const next = new Map(this.entries);
    for (const [node, counter] of other.entries) {
      if (counter > (next.get(node) ?? 0)) next.set(node, counter);
    }
    return new VersionVector(next);
  }

  get size(): number {
    return this.entries.size;
  }

  equals(other: VersionVector): boolean {
    if (this.entries.size !== other.entries.size) return false;
    for (const [node, counter] of this.entries) {
      if (other.get(node) !== counter) return false;
    }
    return true;
  }

  toJSON(): Record<string, number> {
    return Object.fromEntries([...this.entries].sort());
  }
}
