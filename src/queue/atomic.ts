/**
 * CAS (compare-and-swap) primitives.
 *
 * AtomicCounter is backed by a SharedArrayBuffer and uses the `Atomics`
 * API, so it performs genuine hardware-level atomic operations that are
 * safe to share across worker threads — no locks anywhere.
 *
 * AtomicRef provides the same compare-and-swap semantics for plain JS
 * object references inside a single agent (guaranteed atomic by the
 * run-to-completion semantics of the JS event loop).
 */

export class AtomicCounter {
  static readonly BYTE_LENGTH = 4;

  private readonly view: Int32Array;

  constructor(buffer?: SharedArrayBuffer, byteOffset = 0) {
    this.view = new Int32Array(
      buffer ?? new SharedArrayBuffer(AtomicCounter.BYTE_LENGTH),
      byteOffset,
      1
    );
  }

  static create(initial = 0): AtomicCounter {
    const c = new AtomicCounter();
    c.store(initial);
    return c;
  }

  get buffer(): SharedArrayBuffer {
    return this.view.buffer as SharedArrayBuffer;
  }

  load(): number {
    return Atomics.load(this.view, 0);
  }

  store(v: number): void {
    Atomics.store(this.view, 0, v);
  }

  /** Atomically add `delta`; returns the previous value. */
  add(delta: number): number {
    return Atomics.add(this.view, 0, delta);
  }

  /** Atomically replace `expected` with `desired`; returns the previous value. */
  compareExchange(expected: number, desired: number): number {
    return Atomics.compareExchange(this.view, 0, expected, desired);
  }

  /** CAS returning whether the swap happened. */
  cas(expected: number, desired: number): boolean {
    return this.compareExchange(expected, desired) === expected;
  }
}

export class AtomicRef<T> {
  private value: T;

  constructor(initial: T) {
    this.value = initial;
  }

  load(): T {
    return this.value;
  }

  store(v: T): void {
    this.value = v;
  }

  /**
   * Compare-and-swap on an object reference. Within one JS agent the
   * check-and-store sequence cannot be interrupted, so it is atomic.
   */
  compareExchange(expected: T, desired: T): T {
    const prev = this.value;
    if (prev === expected) this.value = desired;
    return prev;
  }

  cas(expected: T, desired: T): boolean {
    return this.compareExchange(expected, desired) === expected;
  }
}
