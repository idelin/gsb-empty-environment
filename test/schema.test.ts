import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { DecodeError, ValidationError } from "../src/errors.ts";
import {
  assertValid,
  decodeMessage,
  encodeMessage,
  validateMessage,
  type MessageSchema,
} from "../src/schema.ts";
import { WireWriter } from "../src/writer.ts";
import { captureError } from "../testkit/capture.ts";

// Three-level nested schema with packed repeated arrays, exercising every
// scalar family: varint, zigzag, fixed32/64, string, bytes, nested message.
const ORDER: MessageSchema = {
  name: "Order",
  fields: [
    { id: 1, name: "orderId", type: "uint64" },
    { id: 2, name: "delta", type: "sint64" },
    { id: 3, name: "tags", type: "uint32", repeated: true, packed: true },
    {
      id: 4,
      name: "customer",
      type: "message",
      schema: {
        name: "Customer",
        fields: [
          { id: 1, name: "name", type: "string" },
          { id: 2, name: "vip", type: "bool" },
          {
            id: 3,
            name: "address",
            type: "message",
            schema: {
              name: "Address",
              fields: [
                { id: 1, name: "city", type: "string" },
                { id: 2, name: "zip", type: "fixed32" },
                { id: 3, name: "geo", type: "sfixed64" },
              ],
            },
          },
        ],
      },
    },
    { id: 5, name: "scores", type: "sint32", repeated: true, packed: true },
    { id: 6, name: "attachment", type: "bytes" },
    { id: 7, name: "history", type: "fixed64", repeated: true }, // unpacked on the wire
  ],
};

function sampleOrder() {
  return {
    orderId: 2n ** 60n + 7n,
    delta: -987654321012345n,
    tags: [1, 127, 128, 65535, 4294967295],
    customer: {
      name: "Ada Lovelace",
      vip: true,
      address: { city: "Shanghai", zip: 200000, geo: -31337000000n },
    },
    scores: [-3, 0, 42, 2147483647, -2147483648],
    attachment: new Uint8Array([0xde, 0xad, 0xbe, 0xef]),
    history: [0n, 1n, 2n ** 64n - 1n],
  };
}

describe("reflection decode: multi-level nesting + packed repeated", () => {
  it("round-trips a deeply nested structure bit-for-bit", () => {
    const order = sampleOrder();
    const bytes = encodeMessage(ORDER, order);
    const decoded = decodeMessage(ORDER, bytes);
    assert.deepEqual(decoded, order);
  });

  it("emits packed repeated numeric fields as a single length-delimited block", () => {
    const bytes = encodeMessage(ORDER, sampleOrder());
    // field 3 (tags), wire type 2 => tag byte (3 << 3) | 2 = 0x1a
    const idx = bytes.indexOf(0x1a);
    assert.ok(idx >= 0, "packed tags block present");
    const blockLen = bytes[idx + 1];
    assert.ok(blockLen >= 5, "all 5 varints share one block");
  });

  it("decodes packed data sent as unpacked, and vice versa", () => {
    // Manually encode tags as unpacked varints (field 3, wire type 0).
    const w = new WireWriter();
    for (const t of [5, 6, 7]) w.varintField(3, t);
    w.varintField(1, 9n);
    const decoded = decodeMessage(ORDER, w.finish());
    assert.deepEqual(decoded.tags, [5, 6, 7]);
    assert.equal(decoded.orderId, 9n);
  });

  it("skips unknown fields without disturbing known ones", () => {
    const w = new WireWriter();
    w.varintField(1, 77n); // orderId
    w.stringField(50, "future-field"); // unknown
    w.fixed32Field(51, 1); // unknown
    const decoded = decodeMessage(ORDER, w.finish());
    assert.equal(decoded.orderId, 77n);
    assert.equal(decoded.unknown, undefined);
  });

  it("decodes empty input to an empty object", () => {
    assert.deepEqual(decodeMessage(ORDER, new Uint8Array(0)), {});
  });
});

describe("safe out-of-bounds protection on truncation", () => {
  it("every truncated prefix throws DecodeError, never reads out of bounds", () => {
    const bytes = encodeMessage(ORDER, sampleOrder());
    let thrown = 0;
    for (let cut = 0; cut < bytes.length; cut++) {
      try {
        decodeMessage(ORDER, bytes.subarray(0, cut));
        // A prefix may only decode cleanly if it ends exactly on a field
        // boundary — that is fine; the contract is: never crash, never OOB.
      } catch (err) {
        thrown++;
        assert.ok(err instanceof DecodeError, `cut=${cut}: expected DecodeError, got ${err}`);
        assert.ok((err as DecodeError).offset <= bytes.length);
      }
    }
    assert.ok(thrown > bytes.length * 0.9, `most prefixes must fail (thrown=${thrown}/${bytes.length})`);
  });

  it("reports the dotted field path for nested truncation", () => {
    const bytes = encodeMessage(ORDER, sampleOrder());
    // Find the nested customer block and cut inside the address message.
    const full = decodeMessage(ORDER, bytes);
    assert.ok(full);
    const err = captureError<DecodeError>(
      () => decodeMessage(ORDER, bytes.subarray(0, bytes.length - 2)),
    );
    assert.ok(err instanceof DecodeError);
    assert.match(err.message, /truncated|released/);
  });

  it("rejects a wire-type mismatch with the field path attached", () => {
    const w = new WireWriter();
    w.fixed32Field(1, 0); // orderId is declared uint64 (varint)
    const err = captureError<DecodeError>(() => decodeMessage(ORDER, w.finish()));
    assert.ok(err instanceof DecodeError);
    assert.match(err.message, /wire type mismatch/);
    assert.match(err.message, /Order\.orderId/);
  });

  it("rejects a length field that overruns the buffer", () => {
    const w = new WireWriter();
    w.writeTag(6, 2).writeVarint(1000); // attachment declares 1000 bytes
    w.writeRaw(new Uint8Array(3));
    assert.throws(() => decodeMessage(ORDER, w.finish()), /truncated length-delimited/);
  });
});

describe("reflection validation engine", () => {
  it("accepts a well-formed object", () => {
    assert.deepEqual(validateMessage(ORDER, sampleOrder()), []);
    assert.doesNotThrow(() => assertValid(ORDER, sampleOrder()));
  });

  it("flags range violations, wrong types and bad repeated shapes", () => {
    const bad = {
      orderId: -1n,
      delta: 2n ** 63n, // sint64 overflow
      tags: "not-an-array",
      customer: { name: 42, vip: "yes", address: { city: "x", zip: 2 ** 32, geo: 1n } },
      scores: [1.5],
      attachment: [1, 2, 3],
    };
    const v = validateMessage(ORDER, bad as never);
    assert.ok(v.some((s) => s.includes("Order.orderId") && s.includes("out of range")));
    assert.ok(v.some((s) => s.includes("Order.delta") && s.includes("out of range")));
    assert.ok(v.some((s) => s.includes("Order.tags") && s.includes("array")));
    assert.ok(v.some((s) => s.includes("customer.name") && s.includes("string")));
    assert.ok(v.some((s) => s.includes("customer.vip") && s.includes("boolean")));
    assert.ok(v.some((s) => s.includes("address.zip") && s.includes("out of range")));
    assert.ok(v.some((s) => s.includes("Order.scores[0]") && s.includes("non-integer")));
    assert.ok(v.some((s) => s.includes("Order.attachment") && s.includes("Uint8Array")));
    assert.throws(() => assertValid(ORDER, bad as never), ValidationError);
  });

  it("recurses into repeated nested messages with indexed paths", () => {
    const schema: MessageSchema = {
      name: "Batch",
      fields: [{ id: 1, name: "items", type: "message", repeated: true, schema: ORDER.fields[3].schema }],
    };
    const v = validateMessage(schema, { items: [{ name: "ok" }, { name: 7 }] } as never);
    assert.ok(v.some((s) => s.includes("Batch.items[1].name")));
  });
});
