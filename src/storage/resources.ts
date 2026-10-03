/**
 * Process-local resource accounting used by tests to assert strict absence
 * of leaks: every opened store/segment must be closed before a test ends.
 */
export class ResourceTracker {
  #counts = new Map<string, number>();

  open(kind: string): void {
    this.#counts.set(kind, (this.#counts.get(kind) ?? 0) + 1);
  }

  close(kind: string): void {
    const n = (this.#counts.get(kind) ?? 0) - 1;
    if (n < 0) throw new Error(`resource underflow: ${kind}`);
    if (n === 0) this.#counts.delete(kind);
    else this.#counts.set(kind, n);
  }

  outstanding(kind: string): number {
    return this.#counts.get(kind) ?? 0;
  }

  get totalOutstanding(): number {
    let n = 0;
    for (const v of this.#counts.values()) n += v;
    return n;
  }

  assertDrained(): void {
    if (this.totalOutstanding !== 0) {
      const detail = [...this.#counts.entries()].map(([k, v]) => `${k}=${v}`).join(', ');
      throw new Error(`resource leak detected: ${detail}`);
    }
  }
}

export const resources = new ResourceTracker();
