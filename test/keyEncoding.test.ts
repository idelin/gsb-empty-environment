import { describe, it } from "node:test";
import assert from "node:assert/strict";

import { decodeCompositeKey, encodeCompositeKey } from "../src/index.ts";

const OPTS = { nullsEqual: true, recordIndex: 0 };

describe("composite key encoding", () => {
  it("does not collide when values contain the separator", () => {
    const a = encodeCompositeKey(["x|y", "z"], OPTS);
    const b = encodeCompositeKey(["x", "y|z"], OPTS);
    const c = encodeCompositeKey(["x|y|z"], OPTS);
    const d = encodeCompositeKey(["x", "y", "z"], OPTS);
    const all = new Set([a, b, c, d]);
    assert.equal(all.size, 4);
  });

  it("does not collide on values containing colons or length-like prefixes", () => {
    const a = encodeCompositeKey(["S3:abc"], OPTS);
    const b = encodeCompositeKey(["abc"], OPTS);
    assert.notEqual(a, b);
    assert.deepEqual(decodeCompositeKey(a), ["S3:abc"]);
  });

  it("keeps same-looking values of different types distinct", () => {
    const encoded = [
      encodeCompositeKey([1], OPTS),
      encodeCompositeKey(["1"], OPTS),
      encodeCompositeKey([true], OPTS),
      encodeCompositeKey([null], OPTS),
      encodeCompositeKey([undefined], OPTS),
    ];
    assert.equal(new Set(encoded).size, encoded.length);
  });

  it("canonicalizes numbers: 1, 1.0 and -0 collapse", () => {
    const a = encodeCompositeKey([1], OPTS);
    const b = encodeCompositeKey([1.0], OPTS);
    const c = encodeCompositeKey([-0], OPTS);
    const d = encodeCompositeKey([0], OPTS);
    assert.equal(a, b);
    assert.notEqual(a, c);
    assert.equal(c, d);
  });

  it("treats nulls as equal when nullsEqual is true", () => {
    const a = encodeCompositeKey([null, 5], { nullsEqual: true, recordIndex: 0 });
    const b = encodeCompositeKey([null, 5], { nullsEqual: true, recordIndex: 9 });
    assert.equal(a, b);
  });

  it("treats nulls as distinct when nullsEqual is false", () => {
    const a = encodeCompositeKey([null, 5], { nullsEqual: false, recordIndex: 0 });
    const b = encodeCompositeKey([null, 5], { nullsEqual: false, recordIndex: 9 });
    assert.notEqual(a, b);
  });

  it("round-trips through decodeCompositeKey", () => {
    const values = ["a|b:c", 42, -3.5, true, false, null, undefined, "多字节🎉"];
    const encoded = encodeCompositeKey(values, OPTS);
    const decoded = decodeCompositeKey(encoded);
    assert.deepEqual(decoded, values);
  });

  it("handles empty string components", () => {
    const encoded = encodeCompositeKey(["", ""], OPTS);
    assert.deepEqual(decodeCompositeKey(encoded), ["", ""]);
    assert.notEqual(encoded, encodeCompositeKey([""], OPTS));
  });
});
