import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { decodeMessage, encodeMessage, validateMessage } from "../src/codec-core.ts";
import { CodecError, MalformedError, SchemaValidationError, TruncationError } from "../src/errors.ts";
import type { MessageSchema } from "../src/schema.ts";

function capture(fn: () => unknown): Error {
  try {
    fn();
  } catch (err) {
    return err as Error;
  }
  throw new Error("expected function to throw");
}

/** Three-level nested schema exercising every wire type. */
const Point: MessageSchema = {
  name: "Point",
  fields: [
    { no: 1, name: "x", type: "sint32", required: true },
    { no: 2, name: "y", type: "sint32", required: true },
  ],
};

const Track: MessageSchema = {
  name: "Track",
  fields: [
    { no: 1, name: "id", type: "uint64", required: true },
    { no: 2, name: "label", type: "string" },
    { no: 3, name: "points", type: "message", message: Point, repeated: true },
    { no: 4, name: "samples", type: "sint64", repeated: true, packed: true },
    { no: 5, name: "flags", type: "bool", repeated: true, packed: true },
    { no: 6, name: "bbox", type: "fixed32", repeated: true, packed: true },
  ],
};

const Session: MessageSchema = {
  name: "Session",
  fields: [
    { no: 1, name: "sessionId", type: "fixed64", required: true },
    { no: 2, name: "track", type: "message", message: Track, required: true },
    { no: 3, name: "score", type: "double" },
    { no: 4, name: "ratio", type: "float" },
    { no: 5, name: "blob", type: "bytes" },
    { no: 6, name: "delta", type: "sint64" },
    { no: 7, name: "kind", type: "enum" },
    { no: 8, name: "counter", type: "int32" },
    { no: 9, name: "bigCount", type: "int64" },
  ],
};

const sample = {
  sessionId: 0xffff_ffff_ffff_fff0n,
  track: {
    id: 18_446_744_073_709_551_615n, // uint64 max
    label: "多边形轨迹 🛰",
    points: [
      { x: 0, y: 0 },
      { x: -2_147_483_648, y: 2_147_483_647 },
      { x: 123, y: -456 },
    ],
    samples: [0n, -1n, 1n, -(1n << 62n), (1n << 62n)],
    flags: [true, false, true, true],
    bbox: [0, 123_456_789, 4_294_967_295],
  },
  score: -0.125,
  ratio: 3.5,
  blob: new Uint8Array([0, 1, 2, 250, 251, 255]),
  delta: -9_223_372_036_854_775_808n, // int64 min
  kind: -1, // negative enum -> 10-byte varint
  counter: -2_147_483_648,
  bigCount: 9_223_372_036_854_775_807n, // int64 max
};

describe("reflection codec: nested messages + packed repeated", () => {
  it("round-trips a three-level message with all wire types", () => {
    const bytes = encodeMessage(Session, sample);
    const back = decodeMessage(Session, bytes);
    assert.deepEqual(back, sample);
  });

  it("encodes packed repeated fields as a single length-delimited block", () => {
    const bytes = encodeMessage(Track, {
      id: 1n,
      samples: [1n, 2n, 3n],
    });
    // tag(4, LEN)=0x22, len=3, zigzag: 2,4,6
    const hex = [...bytes].map((b) => b.toString(16).padStart(2, "0")).join(" ");
    assert.match(hex, /22 03 02 04 06/);
  });

  it("accepts packed wire data for an unpacked schema field (proto3 rule)", () => {
    const packed = encodeMessage(Track, { id: 1n, samples: [7n, -8n] });
    const unpackedSchema: MessageSchema = {
      name: "Track",
      fields: [
        { no: 1, name: "id", type: "uint64" },
        { no: 4, name: "samples", type: "sint64", repeated: true }, // not packed
      ],
    };
    const back = decodeMessage(unpackedSchema, packed);
    assert.deepEqual(back.samples, [7n, -8n]);
  });

  it("accepts unpacked wire data for a packed schema field", () => {
    const unpacked: MessageSchema = {
      name: "T",
      fields: [{ no: 1, name: "v", type: "sint32", repeated: true }],
    };
    const packed: MessageSchema = {
      name: "T",
      fields: [{ no: 1, name: "v", type: "sint32", repeated: true, packed: true }],
    };
    const bytes = encodeMessage(unpacked, { v: [5, -5, 100] });
    const back = decodeMessage(packed, bytes);
    assert.deepEqual(back.v, [5, -5, 100]);
  });

  it("empty repeated fields are omitted from the wire", () => {
    const bytes = encodeMessage(Track, { id: 9n, samples: [], points: [] });
    const back = decodeMessage(Track, bytes);
    assert.deepEqual(back, { id: 9n });
  });

  it("records unknown fields without failing", () => {
    const extended: MessageSchema = {
      name: "Ext",
      fields: [
        { no: 1, name: "id", type: "uint64" },
        { no: 99, name: "extra", type: "string" },
      ],
    };
    const bytes = encodeMessage(extended, { id: 5n, extra: "hello" });
    const back = decodeMessage(
      { name: "Base", fields: [{ no: 1, name: "id", type: "uint64" }] },
      bytes,
    );
    assert.equal(back.id, 5n);
    assert.ok(back.$unknown?.[99]);
  });

  it("rejects wire-type mismatches with a descriptive error", () => {
    // Encode a string where the schema expects varint.
    const w = encodeMessage(
      { name: "S", fields: [{ no: 1, name: "s", type: "string" }] },
      { s: "abc" },
    );
    assert.throws(
      () => decodeMessage({ name: "V", fields: [{ no: 1, name: "s", type: "int32" }] }, w),
      /wire type 2, expected 0/,
    );
  });
});

describe("truncation safety: every strict prefix must fail safely", () => {
  const bytes = encodeMessage(Session, sample);

  it("never reads out of bounds for any truncated prefix", () => {
    let truncations = 0;
    let cleanBoundaryCuts = 0;
    for (let cut = 0; cut < bytes.length; cut++) {
      const prefix = bytes.subarray(0, cut);
      try {
        decodeMessage(Session, prefix);
        // Only reachable when the cut lands exactly on a field boundary
        // after every required field: a valid, shorter stream. Not a
        // safety violation.
        cleanBoundaryCuts++;
      } catch (err) {
        if (err instanceof TruncationError) truncations++;
        assert.ok(
          err instanceof CodecError,
          `prefix ${cut}: expected CodecError, got ${String(err)}`,
        );
        // Must never be a raw engine RangeError/TypeError from OOB access.
        assert.ok(!(err instanceof RangeError), `prefix ${cut}: raw RangeError`);
      }
    }
    assert.ok(truncations > 0, "mid-field cuts must raise TruncationError");
    assert.ok(cleanBoundaryCuts < bytes.length, "not all prefixes may decode");
  });

  it("mid-field truncation raises TruncationError with field path", () => {
    // Cut inside the top-level "blob" payload: outer lengths still fit,
    // so the error must be attributed to the blob field itself.
    const blobBytes = new Uint8Array([0, 1, 2, 250, 251, 255]);
    const idx = bytes.findIndex((_, i) => blobBytes.every((v, j) => bytes[i + j] === v));
    assert.ok(idx > 0, "blob payload located in stream");
    const err = capture(() => decodeMessage(Session, bytes.subarray(0, idx + 2)));
    assert.ok(err instanceof TruncationError, `got ${String(err)}`);
    assert.match(err.message, /Session\.blob/);
  });

  it("truncation inside a nested message is reported at its boundary", () => {
    // Cutting inside nested content makes the embedded length prefix
    // unsatisfiable; the error names the nested field (Session.track).
    const labelBytes = new TextEncoder().encode("多边形轨迹 🛰");
    const idx = bytes.findIndex((_, i) => labelBytes.every((v, j) => bytes[i + j] === v));
    assert.ok(idx > 0, "label payload located in stream");
    const err = capture(() => decodeMessage(Session, bytes.subarray(0, idx + 2)));
    assert.ok(err instanceof TruncationError, `got ${String(err)}`);
    assert.match(err.message, /Session\.track/);
  });

  it("truncated packed block raises TruncationError", () => {
    const track = encodeMessage(Track, { id: 1n, samples: [1n, 2n, 3n, 4n] });
    const tagIdx = track.findIndex((b) => b === 0x22);
    assert.ok(tagIdx >= 0);
    const err = capture(() => decodeMessage(Track, track.subarray(0, tagIdx + 3)));
    assert.ok(err instanceof TruncationError, `got ${String(err)}`);
    assert.match(err.message, /samples/);
  });

  it("truncated nested message length prefix raises TruncationError", () => {
    // Declare a huge embedded message length with almost no payload.
    const crafted = new Uint8Array([0x12, 0x7f, 0x08]); // field 2 LEN=127, 1 byte
    assert.throws(() => decodeMessage(Session, crafted), TruncationError);
  });
});

describe("schema validation engine", () => {
  it("rejects missing required fields on encode", () => {
    assert.throws(() => encodeMessage(Point, { x: 1 }), SchemaValidationError);
  });

  it("rejects missing required fields on decode", () => {
    const bytes = encodeMessage(Point, { x: 1, y: 2 });
    const half = bytes.subarray(0, 2); // only field x
    assert.throws(() => decodeMessage(Point, half), SchemaValidationError);
    // ...but succeeds when the check is disabled
    const partial = decodeMessage(Point, half, { checkRequired: false });
    assert.deepEqual(partial, { x: 1 });
  });

  it("reports range violations with field coordinates", () => {
    const err = capture(() => validateMessage(Point, { x: 3_000_000_000, y: 0 }));
    assert.ok(err instanceof SchemaValidationError);
    assert.match(err.message, /Point\.x \(#1\)/);
  });

  it("rejects wrong container types", () => {
    assert.throws(
      () => validateMessage(Track, { id: 1n, samples: "nope" }),
      /expected array/,
    );
    assert.throws(
      () => validateMessage(Track, { id: 1n, label: 42 }),
      /expected string/,
    );
  });

  it("rejects packing of non-packable types", () => {
    const bad: MessageSchema = {
      name: "Bad",
      fields: [{ no: 1, name: "s", type: "string", repeated: true, packed: true }],
    };
    assert.throws(() => validateMessage(bad, { s: ["a"] }), /not packable/);
  });

  it("rejects malformed wire data (overflowing varint, bad tags)", () => {
    const overflow = new Uint8Array([0x08, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f]);
    assert.throws(
      () => decodeMessage({ name: "M", fields: [{ no: 1, name: "v", type: "uint64" }] }, overflow),
      MalformedError,
    );
    const zeroField = new Uint8Array([0x00, 0x01]);
    assert.throws(
      () => decodeMessage({ name: "M", fields: [] }, zeroField),
      MalformedError,
    );
  });
});
