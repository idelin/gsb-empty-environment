/**
 * WireEngine: ties the zero-alloc cache and object pools to the
 * schema-driven decoder for GC-pause-free hot paths.
 *
 * - Readers/writers are pooled and reset, never re-allocated per message.
 * - decode() reuses a caller-supplied target graph, so steady-state
 *   parsing of nested messages + packed arrays performs zero heap
 *   allocation (verified in tests via pool stats and object identity).
 * - An optional ZeroAllocCache memoizes decoded messages by numeric key
 *   (e.g. a message id field) without per-lookup allocation.
 */
import { ZeroAllocCache } from "./cache.ts";
import { ObjectPool } from "./pool.ts";
import { WireReader } from "./reader.ts";
import { decodeMessage } from "./schema.ts";
import type { MessageSchema } from "./schema.ts";
import { WireWriter } from "./writer.ts";

const EMPTY = new Uint8Array(0);

export class WireEngine {
  readonly readerPool = new ObjectPool<WireReader>(
    () => new WireReader(),
    (r) => r.reset(EMPTY),
    256,
  );
  readonly writerPool = new ObjectPool<WireWriter>(
    () => new WireWriter(),
    (w) => w.reset(),
    64,
  );
  readonly cache: ZeroAllocCache<Record<string, unknown>>;

  constructor(cacheCapacity = 256) {
    this.cache = new ZeroAllocCache(cacheCapacity);
  }

  /**
   * Decode `buf` per `schema`. Reader instances come from the pool and are
   * always returned (even on error), keeping the pool leak-free.
   */
  decode(
    buf: Uint8Array,
    schema: MessageSchema,
    target?: Record<string, unknown>,
  ): Record<string, unknown> {
    const reader = this.readerPool.acquire();
    try {
      reader.reset(buf);
      return decodeMessage(reader, schema, target);
    } finally {
      this.readerPool.release(reader);
    }
  }

  /** Cache-aware decode: returns a memoized result for `key` when present. */
  decodeCached(
    key: number,
    buf: Uint8Array,
    schema: MessageSchema,
  ): Record<string, unknown> {
    return this.cache.getOrLoad(key, () => this.decode(buf, schema));
  }

  /** Run `fn` with a pooled writer; the writer is always released. */
  withWriter<T>(fn: (w: WireWriter) => T): T {
    const writer = this.writerPool.acquire();
    try {
      return fn(writer);
    } finally {
      this.writerPool.release(writer);
    }
  }

  /** Strict leak assertion across all pools. */
  get leakFree(): boolean {
    return this.readerPool.leakFree && this.writerPool.leakFree;
  }
}
