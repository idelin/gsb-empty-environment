/**
 * Vectorized operators in a pull-based (Volcano-style) execution model.
 *
 * Every operator pulls whole chunks from its child, transforms the data
 * IN PLACE inside the chunk's pre-allocated buffer, and hands the same
 * chunk upstream — no per-row or per-chunk heap allocation. Chunks are
 * only created by `ScanSource` (from the pool) and by fan-out operators
 * (from the pool), and are returned to the pool by the sink or by the
 * operator that consumed them.
 */

import { ChunkPool } from './pool.ts';
import type { Chunk } from './pool.ts';
import { MemoryMonitor } from './monitor.ts';

export interface Operator {
  /** Returns the next chunk, or null when the input is exhausted. */
  next(): Chunk | null;
}

/** Operator 0 (source): scans a synthetic row range into pooled chunks. */
export class ScanSource implements Operator {
  private readonly pool: ChunkPool;
  private readonly totalRows: number;
  private emitted = 0;

  constructor(pool: ChunkPool, totalRows: number) {
    this.pool = pool;
    this.totalRows = totalRows;
  }

  next(): Chunk | null {
    if (this.emitted >= this.totalRows) return null;
    const chunk = this.pool.acquire();
    const remaining = this.totalRows - this.emitted;
    const n = remaining < this.pool.chunkSize ? remaining : this.pool.chunkSize;
    const data = chunk.data;
    const base = this.emitted;
    for (let i = 0; i < n; i++) {
      data[i] = base + i;
    }
    chunk.length = n;
    this.emitted += n;
    return chunk;
  }
}

/** Base class for in-place transforms: same chunk in, same chunk out. */
export abstract class Transform implements Operator {
  protected readonly child: Operator;

  constructor(child: Operator) {
    this.child = child;
  }

  next(): Chunk | null {
    const chunk = this.child.next();
    if (chunk === null) return null;
    this.apply(chunk);
    return chunk;
  }

  protected abstract apply(chunk: Chunk): void;
}

/** Operator 1: selection — compacts matching rows in place. */
export class FilterOperator extends Transform {
  private readonly modulus: number;

  constructor(child: Operator, modulus: number) {
    super(child);
    this.modulus = modulus;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const m = this.modulus;
    let w = 0;
    for (let i = 0; i < chunk.length; i++) {
      const v = d[i];
      if (v % m !== 0) {
        d[w] = v;
        w++;
      }
    }
    chunk.length = w;
  }
}

/** Operator 2: projection — v + c. */
export class AddConstantOperator extends Transform {
  private readonly c: number;

  constructor(child: Operator, c: number) {
    super(child);
    this.c = c;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const c = this.c;
    for (let i = 0; i < chunk.length; i++) d[i] += c;
  }
}

/** Operator 3: projection — v * c. */
export class MultiplyOperator extends Transform {
  private readonly c: number;

  constructor(child: Operator, c: number) {
    super(child);
    this.c = c;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const c = this.c;
    for (let i = 0; i < chunk.length; i++) d[i] *= c;
  }
}

/** Operator 4: math — sqrt(v). */
export class SqrtOperator extends Transform {
  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    for (let i = 0; i < chunk.length; i++) d[i] = Math.sqrt(d[i]);
  }
}

/** Operator 5: clamp to [min, max]. */
export class ClampOperator extends Transform {
  private readonly min: number;
  private readonly max: number;

  constructor(child: Operator, min: number, max: number) {
    super(child);
    this.min = min;
    this.max = max;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const lo = this.min;
    const hi = this.max;
    for (let i = 0; i < chunk.length; i++) {
      const v = d[i];
      d[i] = v < lo ? lo : v > hi ? hi : v;
    }
  }
}

/** Operator 6: floating-point modulo. */
export class ModuloOperator extends Transform {
  private readonly m: number;

  constructor(child: Operator, m: number) {
    super(child);
    this.m = m;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const m = this.m;
    for (let i = 0; i < chunk.length; i++) d[i] = d[i] % m;
  }
}

/** Operator 7: abs. */
export class AbsOperator extends Transform {
  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    for (let i = 0; i < chunk.length; i++) d[i] = Math.abs(d[i]);
  }
}

/** Operator 8: fused scale-and-offset — a * v + b. */
export class ScaleOffsetOperator extends Transform {
  private readonly a: number;
  private readonly b: number;

  constructor(child: Operator, a: number, b: number) {
    super(child);
    this.a = a;
    this.b = b;
  }

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    const a = this.a;
    const b = this.b;
    for (let i = 0; i < chunk.length; i++) d[i] = a * d[i] + b;
  }
}

/** Operator 9: stateful running (prefix) sum carried across chunks. */
export class RunningSumOperator extends Transform {
  private carry = 0;

  protected apply(chunk: Chunk): void {
    const d = chunk.data;
    let prev = this.carry;
    for (let i = 0; i < chunk.length; i++) {
      prev += d[i];
      d[i] = prev;
    }
    this.carry = prev;
  }
}

/**
 * Operator 10: fan-out — emits each input chunk twice (second copy
 * shifted by +1). Acquires output chunks from the pool and releases the
 * consumed input chunk back to the pool, exercising mid-pipeline
 * acquire/release slot reuse.
 */
export class ReplicateOperator implements Operator {
  private readonly child: Operator;
  private readonly pool: ChunkPool;
  private input: Chunk | null = null;
  private phase = 0;

  constructor(child: Operator, pool: ChunkPool) {
    this.child = child;
    this.pool = pool;
  }

  next(): Chunk | null {
    if (this.input === null) {
      this.input = this.child.next();
      if (this.input === null) return null;
      this.phase = 0;
    }
    const input = this.input;
    const out = this.pool.acquire();
    const src = input.data;
    const dst = out.data;
    const n = input.length;
    const offset = this.phase;
    for (let i = 0; i < n; i++) {
      dst[i] = src[i] + offset;
    }
    out.length = n;
    if (this.phase === 1) {
      this.pool.release(input);
      this.input = null;
    }
    this.phase = 1 - this.phase;
    return out;
  }
}

/** Terminal operator: aggregates and returns every chunk to the pool. */
export class SumSink {
  private readonly child: Operator;
  private readonly pool: ChunkPool;
  private readonly monitor: MemoryMonitor | null;
  private readonly sampleEvery: number;
  private sinceSample = 0;
  private chunksProcessed = 0;
  private rowsProcessed = 0;

  constructor(
    child: Operator,
    pool: ChunkPool,
    monitor?: MemoryMonitor,
    sampleEvery = 1024,
  ) {
    this.child = child;
    this.pool = pool;
    this.monitor = monitor ?? null;
    this.sampleEvery = sampleEvery;
  }

  run(): number {
    let sum = 0;
    for (;;) {
      const chunk = this.child.next();
      if (chunk === null) break;
      const d = chunk.data;
      const n = chunk.length;
      for (let i = 0; i < n; i++) sum += d[i];
      this.rowsProcessed += n;
      this.chunksProcessed++;
      this.pool.release(chunk);
      if (this.monitor !== null && ++this.sinceSample >= this.sampleEvery) {
        this.sinceSample = 0;
        this.monitor.sample();
      }
    }
    return sum;
  }

  get chunks(): number {
    return this.chunksProcessed;
  }

  get rows(): number {
    return this.rowsProcessed;
  }
}
