import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  TruncatedFieldError,
  WireTypeMismatchError,
} from "../src/errors.ts";
import { WireReader } from "../src/reader.ts";
import { compileSchema, decodeMessage } from "../src/schema.ts";
import type { MessageSchema } from "../src/schema.ts";
import { WireWriter } from "../src/writer.ts";

/* message Inner  { sint32 delta = 1; fixed64 stamp = 2; }
 * message Middle { string label = 1; repeated sint32 scores = 2 [packed];
 *                  Inner inner = 3; repeated uint32 flags = 4; }
 * message Outer  { uint32 id = 1; Middle mid = 2;
 *                  repeated fixed32 ratios = 3 [packed]; bytes blob = 4; } */
const Inner: MessageSchema = {
  name: "Inner",
  fields: [
    { field: 1, name: "delta", kind: "zigzag32" },
    { field: 2, name: "stamp", kind: "fixed64" },
  ],
};
const Middle: MessageSchema = {
  name: "Middle",
  fields: [
    { field: 1, name: "label", kind: "string" },
    { field: 2, name: "scores", kind: "packed-zigzag32" },
    { field: 3, name: "inner", kind: "message", schema: Inner },
    { field: 4, name: "flags", kind: "varint", repeated: true },
  ],
};
const Outer: MessageSchema = {
  name: "Outer",
  fields: [
    { field: 1, name: "id", kind: "varint" },
    { field: 2, name: "mid", kind: "message", schema: Middle },
    { field: 3, name: "ratios", kind: "packed-fixed32" },
    { field: 4, name: "blob", kind: "bytes" },
  ],
};

function buildFixture(): Uint8Array {
  const w = new WireWriter();
  w.fieldVarint(1, 9001);

  const mid = new WireWriter();
  mid.fieldString(1, "deep");
  const packed = new WireWriter();
  for (const s of [-5, 0, 7, 123456, -987654, 2147483647, -2147483648]) {
    packed.writeZigZag32(s);
  }
  mid.fieldBytes(2, packed.view());
  const inner = new WireWriter();
  inner.fieldZigZag32(1, -42);
  inner.fieldFixed64(2, 0xdeadbeefcafe1234n);
  mid.fieldBytes(3, inner.view());
  mid.fieldVarint(4, 11);
  mid.fieldVarint(4, 22);
  mid.fieldVarint(4, 33);
  w.fieldBytes(2, mid.view());

  const ratios = new WireWriter();
  for (const r of [0, 1, 65536, 4294967295]) ratios.writeFixed32(r);
  w.fieldBytes(3, ratios.view());

  w.fieldBytes(4, new Uint8Array([0xde, 0xad, 0xbe, 0xef]));
  return w.finish();
}

describe("schema-driven decode of nested + packed messages", () => {
  it("restores every field of a 3-level nested structure", () => {
    const msg = decodeMessage(new WireReader(buildFixture()), Outer);
    assert.equal(msg.id, 9001);
    const mid = msg.mid as Record<string, unknown>;
    assert.equal(mid.label, "deep");
    assert.deepEqual(mid.scores, [-5, 0, 7, 123456, -987654, 2147483647, -2147483648]);
    const inner = mid.inner as Record<string, unknown>;
    assert.equal(inner.delta, -42);
    assert.equal(inner.stamp, 0xdeadbeefcafe1234n);
    assert.deepEqual(mid.flags, [11, 22, 33]); // non-packed repeated
    assert.deepEqual(msg.ratios, [0, 1, 65536, 4294967295]);
    assert.deepEqual(Array.from(msg.blob as Uint8Array), [0xde, 0xad, 0xbe, 0xef]);
  });

  it("skips unknown fields of any wire type", () => {
    const w = new WireWriter();
    w.fieldVarint(1, 5);
    w.fieldVarint(99, 12345); // unknown varint
    w.fieldFixed64(98, 1n); // unknown fixed64
    w.fieldString(97, "skip me"); // unknown length-delimited
    w.fieldFixed32(96, 9); // unknown fixed32
    const msg = decodeMessage(new WireReader(w.finish()), Outer);
    assert.equal(msg.id, 5);
    assert.equal(msg.unknown, undefined);
  });

  it("rejects wire type mismatches against the schema", () => {
    const w = new WireWriter();
    w.fieldFixed32(1, 1); // id is declared varint
    assert.throws(
      () => decodeMessage(new WireReader(w.finish()), Outer),
      WireTypeMismatchError,
    );
  });

  it("rejects field number 0", () => {
    const w = new WireWriter();
    w.writeVarint(0); // tag 0 -> field 0
    w.writeVarint(1);
    assert.throws(
      () => decodeMessage(new WireReader(w.finish()), Outer),
      TruncatedFieldError,
    );
  });

  it("guards truncation at every prefix of a valid stream", () => {
    const full = buildFixture();
    let truncatedCount = 0;
    for (let len = 0; len < full.length; len++) {
      // A prefix ending exactly on a field boundary is itself a valid
      // (shorter) message; a prefix cutting INTO a field must raise
      // TruncatedFieldError. Anything else (crash, foreign error) fails.
      try {
        decodeMessage(new WireReader(full.subarray(0, len)), Outer);
      } catch (e) {
        assert.ok(
          e instanceof TruncatedFieldError,
          `prefix len=${len} threw ${(e as Error).constructor.name}: ` +
            (e as Error).message,
        );
        truncatedCount++;
      }
    }
    assert.ok(truncatedCount > 0, "some prefixes cut inside fields");
    // The full buffer decodes cleanly.
    assert.doesNotThrow(() => decodeMessage(new WireReader(full), Outer));
  });

  it("cutting the trailing bytes payload always raises TruncatedFieldError", () => {
    const full = buildFixture();
    // The fixture ends with field 4 (blob, 4 payload bytes + 1 len byte).
    // Removing 1..5 trailing bytes cuts inside that field.
    for (let cut = 1; cut <= 5; cut++) {
      assert.throws(
        () =>
          decodeMessage(
            new WireReader(full.subarray(0, full.length - cut)),
            Outer,
          ),
        TruncatedFieldError,
        `cut=${cut}`,
      );
    }
  });

  it("guards a corrupted length prefix that overruns the buffer", () => {
    const w = new WireWriter();
    w.writeTag(4, 2); // blob, length-delimited
    w.writeVarint(1_000_000); // absurd length
    w.writeBytes(new Uint8Array([1]));
    // note: writeBytes wrote its own length; craft raw instead
    const raw = new Uint8Array([0x22, 0x05, 0x01]); // field 4, len 5, 1 byte present
    assert.throws(
      () => decodeMessage(new WireReader(raw), Outer),
      TruncatedFieldError,
    );
    void w;
  });

  it("reuses the target graph across decodes (zero-alloc steady state)", () => {
    const buf = buildFixture();
    const target = decodeMessage(new WireReader(buf), Outer);
    const midBefore = target.mid;
    const scoresBefore = (midBefore as Record<string, unknown>).scores;
    const innerBefore = (midBefore as Record<string, unknown>).inner;
    const again = decodeMessage(new WireReader(buf), Outer, target);
    assert.equal(again, target); // same root object
    assert.equal(again.mid, midBefore); // same nested object
    assert.equal(
      (again.mid as Record<string, unknown>).scores,
      scoresBefore,
    ); // same array instance
    assert.equal(
      (again.mid as Record<string, unknown>).inner,
      innerBefore,
    );
    assert.deepEqual((again.mid as Record<string, unknown>).scores,
      [-5, 0, 7, 123456, -987654, 2147483647, -2147483648]);
  });

  it("handles empty packed arrays and empty sub-messages", () => {
    const w = new WireWriter();
    w.fieldVarint(1, 1);
    w.fieldBytes(2, new Uint8Array(0)); // empty Middle
    w.fieldBytes(3, new Uint8Array(0)); // empty packed ratios
    const msg = decodeMessage(new WireReader(w.finish()), Outer);
    assert.deepEqual(msg.ratios, []);
    const mid = msg.mid as Record<string, unknown>;
    assert.deepEqual(mid.scores, []);
    assert.deepEqual(mid.flags, []);
  });

  it("compileSchema caches per schema object", () => {
    assert.equal(compileSchema(Outer), compileSchema(Outer));
  });

  it("validates message fields declare a nested schema", () => {
    const bad: MessageSchema = {
      name: "Bad",
      fields: [{ field: 1, name: "x", kind: "message" }],
    };
    assert.throws(() => compileSchema(bad), /nested schema/);
  });
});
