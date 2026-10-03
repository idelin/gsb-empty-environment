/**
 * Lock-free primitives built directly on top of `Atomics`.
 *
 * Every mutating operation is a single atomic instruction (or a CAS retry
 * loop); there is no global mutex anywhere in the hot path.
 */

/** Atomically compare-and-swap `view[index]` from `expected` to `replacement`.
 *  Returns the value that was observed before the operation (like
 *  `Atomics.compareExchange`). Success iff the return value === expected. */
export function compareAndSwap(
  view: Int32Array,
  index: number,
  expected: number,
  replacement: number,
): number {
  return Atomics.compareExchange(view, index, expected, replacement);
}

/** Atomic fetch-and-add. Returns the previous value. */
export function fetchAdd(view: Int32Array, index: number, delta: number): number {
  return Atomics.add(view, index, delta);
}

export function load(view: Int32Array, index: number): number {
  return Atomics.load(view, index);
}

export function store(view: Int32Array, index: number, value: number): void {
  Atomics.store(view, index, value);
}

/** A standalone atomic counter living in (optionally shared) memory. */
export class AtomicCounter {
  readonly buffer: SharedArrayBuffer;
  private readonly view: Int32Array;

  constructor(initial = 0, buffer?: SharedArrayBuffer) {
    this.buffer = buffer ?? new SharedArrayBuffer(4);
    this.view = new Int32Array(this.buffer);
    if (buffer === undefined) Atomics.store(this.view, 0, initial);
  }

  get(): number {
    return Atomics.load(this.view, 0);
  }

  /** Atomically add `delta`, returning the value *after* the addition. */
  add(delta = 1): number {
    return Atomics.add(this.view, 0, delta) + delta;
  }

  /** CAS: returns true when the swap happened. */
  compareAndSwap(expected: number, replacement: number): boolean {
    return Atomics.compareExchange(this.view, 0, expected, replacement) === expected;
  }
}
