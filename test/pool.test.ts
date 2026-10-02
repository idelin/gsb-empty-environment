import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { DoubleReleaseError, ObjectPool } from "../src/pool.ts";
import { WireReader } from "../src/reader.ts";
import { WireEngine } from "../src/engine.ts";

interface Box {
  value: number;
}

function makePool(maxSize = 4) {
  return new ObjectPool<Box>(() => ({ value: 0 }), (b) => {
    b.value = 0;
  }, maxSize);
}

describe("ObjectPool basics", () => {
  it("creates on demand and reuses released instances", () => {
    const pool = makePool();
    const a = pool.acquire();
    const b = pool.acquire();
    assert.equal(pool.stats().created, 2);
    pool.release(a);
    pool.release(b);
    const c = pool.acquire();
    assert.ok(c === a || c === b, "must recycle a released instance");
    assert.equal(pool.stats().reused, 1);
    pool.release(c);
    assert.ok(pool.leakFree);
  });

  it("resets objects on release", () => {
    const pool = makePool();
    const a = pool.acquire();
    a.value = 99;
    pool.release(a);
    const b = pool.acquire();
    assert.equal(b, a);
    assert.equal(b.value, 0, "resetFn must run before reuse");
    pool.release(b);
  });

  it("bounds the free list at maxSize and drops extras", () => {
    const pool = makePool(2);
    const objs = [pool.acquire(), pool.acquire(), pool.acquire(), pool.acquire()];
    for (const o of objs) pool.release(o);
    assert.equal(pool.stats().freeCount, 2);
    assert.ok(pool.leakFree);
  });

  it("supports maxSize = 0 (always allocate, never store)", () => {
    const pool = makePool(0);
    const a = pool.acquire();
    pool.release(a);
    const b = pool.acquire();
    assert.notEqual(a, b);
    assert.equal(pool.stats().freeCount, 0);
    pool.release(b);
    assert.ok(pool.leakFree);
  });

  it("rejects negative maxSize", () => {
    assert.throws(() => makePool(-1), RangeError);
  });

  it("detects double release", () => {
    const pool = makePool();
    const a = pool.acquire();
    pool.release(a);
    assert.throws(() => pool.release(a), DoubleReleaseError);
    // pool stays consistent: the object is still exactly once in the free list
    const b = pool.acquire();
    assert.equal(b, a);
    pool.release(b);
    assert.ok(pool.leakFree);
  });

  it("tracks outstanding precisely (leak detection)", () => {
    const pool = makePool();
    const a = pool.acquire();
    pool.acquire();
    assert.ok(!pool.leakFree);
    assert.equal(pool.stats().outstanding, 2);
    pool.release(a);
    assert.equal(pool.stats().outstanding, 1);
  });
});

describe("ObjectPool concurrency (async interleaving)", () => {
  it("stays leak-free under 64 workers x 1000 interleaved cycles", async () => {
    const pool = makePool(64);
    const workers: Promise<void>[] = [];
    for (let w = 0; w < 64; w++) {
      workers.push((async () => {
        for (let i = 0; i < 1000; i++) {
          const obj = pool.acquire();
          obj.value = w * 1000 + i;
          await Promise.resolve(); // yield: interleave with other workers
          assert.equal(obj.value, w * 1000 + i, "no cross-worker aliasing");
          pool.release(obj);
        }
      })());
    }
    await Promise.all(workers);
    const s = pool.stats();
    assert.equal(s.outstanding, 0, "strict no-leak assertion");
    assert.ok(pool.leakFree);
    assert.ok(s.created <= 64, `pool bounded by worker count, created=${s.created}`);
    assert.ok(s.reused > 60_000, `high reuse ratio, reused=${s.reused}`);
  });

  it("engine pools stay leak-free across decode errors", () => {
    const engine = new WireEngine(8);
    const schema = {
      name: "S",
      fields: [{ field: 1, name: "v", kind: "varint" as const }],
    };
    // 100 failing decodes (truncated) + 100 good ones
    for (let i = 0; i < 100; i++) {
      assert.throws(() => engine.decode(new Uint8Array([0x08]), schema));
      engine.decode(new Uint8Array([0x08, 0x2a]), schema);
    }
    assert.ok(engine.leakFree, "readers returned to pool even on throw");
    assert.equal(engine.readerPool.stats().created, 1);
  });

  it("pooled readers are reset between uses", () => {
    const engine = new WireEngine(8);
    const schema = {
      name: "S",
      fields: [{ field: 1, name: "v", kind: "varint" as const }],
    };
    engine.decode(new Uint8Array([0x08, 0x01, 0x08, 0x02]), schema);
    const out = engine.decode(new Uint8Array([0x08, 0x07]), schema);
    assert.equal(out.v, 7); // no leftover state from previous buffer
    const r = engine.readerPool.acquire();
    assert.ok(r instanceof WireReader);
    engine.readerPool.release(r);
    assert.ok(engine.leakFree);
  });
});
