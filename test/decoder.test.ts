import assert from "node:assert/strict";
import test from "node:test";
import {
  DeterministicDecoder,
  IdentityDecoder,
  driveDecoder,
  encodeDeterministic,
  PipelineError,
} from "../src/index.ts";
import { bytesEqual, text, utf8 } from "./helpers.ts";

test("deterministic decoder works at every two-way cut point", async () => {
  const decoded = text("cut me at every point, including repeats aaaaa bbbbb");
  const encoded = encodeDeterministic(decoded);
  for (let cut = 0; cut <= encoded.byteLength; cut += 1) {
    const decoder = new DeterministicDecoder();
    const output = await driveDecoder(decoder, [
      encoded.slice(0, cut),
      encoded.slice(cut),
    ]);
    assert.equal(bytesEqual(output, decoded), true, `cut ${cut}`);
  }
});

test("single-byte feeds and empty tail blocks are valid", async () => {
  const decoded = text("byte-wise");
  const encoded = encodeDeterministic(decoded, { endMarker: false });
  const emptyTailBlock = new Uint8Array([0x01, 0, 0]);
  const withEmptyTail = new Uint8Array([
    ...encoded,
    ...emptyTailBlock,
    0,
  ]);
  const decoder = new DeterministicDecoder({ requireEndMarker: true });
  const chunks = [...withEmptyTail].map((byte) => new Uint8Array([byte]));
  chunks.push(new Uint8Array(0));
  const output = await driveDecoder(decoder, chunks);
  assert.equal(utf8(output), "byte-wise");
});

test("decoder reports zero consumption while output is pending", () => {
  const firstChunk = new Uint8Array([0x01, 0, 4, 0x61, 0x62, 0x63, 0x64]);
  const secondChunk = new Uint8Array([0x01, 0, 2, 0x65, 0x66]);
  const decoder = new DeterministicDecoder({ maxOutputBuffer: 1 });

  const first = decoder.feed(firstChunk);
  assert.equal(first.consumed, firstChunk.byteLength);
  assert.equal(first.pendingOutput, 4);

  const blocked = decoder.feed(secondChunk);
  assert.equal(blocked.consumed, 0);
  assert.equal(blocked.pendingOutput, 4);

  assert.equal(utf8(decoder.read(1)), "a");
  const stillBlocked = decoder.feed(secondChunk);
  assert.equal(stillBlocked.consumed, 0);

  assert.equal(utf8(decoder.read()), "bcd");
  const retried = decoder.feed(secondChunk);
  assert.equal(retried.consumed, secondChunk.byteLength);
  assert.equal(utf8(decoder.read()), "ef");
  decoder.finish();
});

test("identity decoder also handles zero consumption", () => {
  const decoder = new IdentityDecoder({ maxOutputBuffer: 1 });
  const first = decoder.feed(text("abcd"));
  assert.equal(first.consumed, 4);
  const blocked = decoder.feed(text("ef"));
  assert.equal(blocked.consumed, 0);
  assert.equal(utf8(decoder.read()), "abcd");
  assert.equal(decoder.feed(text("ef")).consumed, 2);
  assert.equal(utf8(decoder.read()), "ef");
});

test("driveDecoder applies asynchronous sink backpressure in order", async () => {
  const decoded = text("backpressure ".repeat(50));
  const encoded = encodeDeterministic(decoded);
  const decoder = new DeterministicDecoder({ maxOutputBuffer: 16 });
  const received: Uint8Array[] = [];
  const output = await driveDecoder(decoder, [encoded], {
    maxChunkBytes: 7,
    collect: false,
    onOutput: async (chunk) => {
      await new Promise((resolve) => setTimeout(resolve, 0));
      received.push(chunk);
    },
  });
  assert.equal(output.byteLength, 0);
  assert.equal(
    utf8(Uint8Array.from(received.flatMap((chunk) => [...chunk]))),
    utf8(decoded),
  );
});

test("driveDecoder cancellation stops late output", async () => {
  const encoded = encodeDeterministic(text("cancel ".repeat(100)));
  const controller = new AbortController();
  const decoder = new DeterministicDecoder({ maxOutputBuffer: 8 });
  let calls = 0;
  await assert.rejects(
    driveDecoder(decoder, [encoded], {
      signal: controller.signal,
      maxChunkBytes: 8,
      onOutput: async () => {
        calls += 1;
        if (calls === 2) {
          controller.abort("caller went away");
        }
      },
    }),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "CANCELLED",
  );
});

test("incomplete block and trailing bytes are rejected", () => {
  const decoder = new DeterministicDecoder();
  decoder.feed(new Uint8Array([0x01, 0, 5, 1, 2]));
  assert.throws(
    () => decoder.finish(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "INCOMPLETE_DECODE",
  );

  const trailing = new DeterministicDecoder();
  trailing.feed(new Uint8Array([0x01, 0, 1, 0x61, 0x00, 0x62]));
  assert.throws(
    () => trailing.finish(),
    (error: unknown) =>
      error instanceof PipelineError && error.code === "INCOMPLETE_DECODE",
  );
});
