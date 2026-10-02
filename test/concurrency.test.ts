import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { decodeMessage, encodeMessage } from "../src/codec-core.ts";
import { SparseVector } from "../src/sparse.ts";
import type { MessageSchema } from "../src/schema.ts";

const Metric: MessageSchema = {
  name: "Metric",
  fields: [
    { no: 1, name: "id", type: "uint64", required: true },
    { no: 2, name: "values", type: "sint64", repeated: true, packed: true },
    { no: 3, name: "tag", type: "string" },
  ],
};

/** Yield to the microtask queue so async tasks interleave. */
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));

describe("concurrency safety", () => {
  it("parallel encode/decode tasks never corrupt each other", async () => {
    const tasks = Array.from({ length: 64 }, async (_, i) => {
      const obj = {
        id: BigInt(i),
        values: Array.from({ length: 50 }, (_, j) => BigInt(i * 1000 + j)),
        tag: `metric-${i}`,
      };
      await tick();
      const bytes = encodeMessage(Metric, obj);
      await tick();
      const back = decodeMessage(Metric, bytes);
      assert.deepEqual(back, obj, `task ${i} round-trip`);
      return bytes.length;
    });
    const sizes = await Promise.all(tasks);
    assert.equal(sizes.length, 64);
  });

  it("interleaved writes/adapts on a shared vector stay consistent", async () => {
    const v = new SparseVector({ denseThreshold: 0.4 });
    const writers = Array.from({ length: 8 }, async (_, w) => {
      for (let k = 0; k < 500; k++) {
        v.set(w * 500 + k, (w + 1) * 1000 + k);
        if (k % 50 === 0) await tick();
      }
    });
    await Promise.all(writers);
    for (let w = 0; w < 8; w++) {
      for (let k = 0; k < 500; k++) {
        assert.equal(v.get(w * 500 + k), (w + 1) * 1000 + k);
      }
    }
    assert.equal(v.size, 4000);
  });

  it("interleaved densify/sparsify cycles end in a consistent state", async () => {
    const v = new SparseVector({ denseThreshold: 0.4 });
    const worker = async (rounds: number) => {
      for (let r = 0; r < rounds; r++) {
        for (let i = 0; i < 200; i++) v.set(i, i + 1);
        await tick();
        for (let i = 0; i < 190; i++) v.delete(i);
        await tick();
      }
    };
    await Promise.all([worker(10), worker(10), worker(10)]);
    // Deterministic final state: indices 190..199 survive with value i+1.
    assert.equal(v.size, 10);
    for (let i = 190; i < 200; i++) assert.equal(v.get(i), i + 1);
    for (let i = 0; i < 190; i++) assert.equal(v.get(i), 0);
  });

  it("no leak: storage returns to baseline after mass clear", async () => {
    const vectors = Array.from({ length: 32 }, () => new SparseVector());
    await Promise.all(
      vectors.map(async (v, i) => {
        for (let k = 0; k < 2000; k++) v.set(k, k + i + 1);
        await tick();
      }),
    );
    const total = vectors.reduce((acc, v) => acc + v.byteSize(), 0);
    assert.ok(total > 0);
    for (const v of vectors) v.clear();
    const after = vectors.reduce((acc, v) => acc + v.byteSize(), 0);
    assert.equal(after, 32 * 64, "every vector must release back to baseline");
  });

  it("decoding does not retain references into the source buffer", async () => {
    const Schema: MessageSchema = {
      name: "Blob",
      fields: [{ no: 1, name: "data", type: "bytes", required: true }],
    };
    const bytes = encodeMessage(Schema, { data: new Uint8Array([1, 2, 3, 4]) });
    const decoded = decodeMessage(Schema, bytes);
    bytes.fill(0xff); // corrupt the source after decoding
    assert.deepEqual([...(decoded.data as Uint8Array)], [1, 2, 3, 4]);
  });
});
