export type ClockSnapshot = Readonly<Record<string, number>>;

/**
 * Vector clock over node ids. Each node increments only its own component;
 * clocks merge by component-wise max (standard CRDT join).
 */
export class VectorClock {
  private readonly entries = new Map<string, number>();

  static from(snapshot: ClockSnapshot): VectorClock {
    const clock = new VectorClock();
    for (const [nodeId, value] of Object.entries(snapshot)) {
      clock.entries.set(nodeId, value);
    }
    return clock;
  }

  static entrywiseMin(clocks: Iterable<VectorClock>): VectorClock {
    const min = new VectorClock();
    const all = [...clocks];
    const keys = new Set<string>();
    for (const clock of all) {
      for (const nodeId of clock.entries.keys()) keys.add(nodeId);
    }
    for (const nodeId of keys) {
      let value = Infinity;
      for (const clock of all) {
        const component = clock.get(nodeId);
        if (component < value) value = component;
      }
      min.entries.set(nodeId, value === Infinity ? 0 : value);
    }
    return min;
  }

  get(nodeId: string): number {
    return this.entries.get(nodeId) ?? 0;
  }

  tick(nodeId: string): number {
    const next = this.get(nodeId) + 1;
    this.entries.set(nodeId, next);
    return next;
  }

  merge(other: VectorClock): void {
    for (const [nodeId, value] of other.entries) {
      if (value > this.get(nodeId)) {
        this.entries.set(nodeId, value);
      }
    }
  }

  /** True when `other` happened-before (or equals) this clock component-wise. */
  dominates(other: VectorClock): boolean {
    for (const [nodeId, value] of other.entries) {
      if (this.get(nodeId) < value) {
        return false;
      }
    }
    return true;
  }

  /** Smallest component value — the scalar "causal watermark". */
  minComponent(): number {
    let min = Infinity;
    for (const value of this.entries.values()) {
      if (value < min) min = value;
    }
    return min === Infinity ? 0 : min;
  }

  nodeIds(): string[] {
    return [...this.entries.keys()];
  }

  snapshot(): ClockSnapshot {
    return Object.fromEntries(this.entries);
  }

  toString(): string {
    const parts = [...this.entries.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([id, v]) => `${id}:${v}`);
    return `{${parts.join(", ")}}`;
  }
}
