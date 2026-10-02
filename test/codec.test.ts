import { test } from "node:test";
import assert from "node:assert/strict";
import {
  encode, decodeNaive, decodeTolerant, findSync,
  SEGMENT_BITS, SEGMENT_BYTES, SYNC_BITS,
} from "../src/codec.ts";
import { deleteBits, flipBit } from "../src/bitstream.ts";
import { SAMPLE_MESSAGE, sampleCodebook, messageToSymbols } from "../src/sample.ts";

const book = sampleCodebook();
const symbols = messageToSymbols(SAMPLE_MESSAGE);
const encoded = encode(symbols, book);

const DELETE_AT_BIT = 400; // byte 50, middle of segment 0
const DELETE_COUNT = 3;

function postCorruptionStats() {
  const lens = symbols.map((s) => book.get(s)!.length);
  const intactPrefix = encoded.symbolBitPositions.filter((p, i) => p + lens[i] <= DELETE_AT_BIT).length;
  const seg1Start = encoded.symbolBitPositions.filter((p) => p < SEGMENT_BITS).length;
  return { intactPrefix, seg1Start, atRisk: symbols.length - intactPrefix };
}

test("clean stream round-trips exactly through the tolerant decoder", () => {
  const { symbols: decoded, resyncEvents } = decodeTolerant(encoded.bits, book);
  assert.deepEqual(decoded, symbols);
  assert.equal(resyncEvents, 0);
});

test("sync words sit at 100-byte boundaries and are found by the bit-level scanner", () => {
  assert.equal(encoded.syncBitPositions[0], 0);
  assert.equal(encoded.syncBitPositions[1], SEGMENT_BITS);
  assert.equal(encoded.syncBitPositions[1] / 8, SEGMENT_BYTES);
  for (const p of encoded.syncBitPositions) {
    assert.equal(findSync(encoded.bits, p), p);
  }
});

test("3-bit deletion: naive decoder garbles everything after the damage", () => {
  const corrupted = deleteBits(encoded.bits, DELETE_AT_BIT, DELETE_COUNT);
  const naive = decodeNaive(corrupted, book);
  const { intactPrefix, atRisk } = postCorruptionStats();
  let match = 0;
  for (let i = intactPrefix; i < Math.min(naive.length, symbols.length); i++) {
    if (naive[i] === symbols[i]) match++;
  }
  assert.ok(match / atRisk < 0.2, `naive decoder should be mostly garbage, got ${match}/${atRisk}`);
});

test("3-bit deletion: tolerant decoder re-locks on the sync word at byte 100", () => {
  const corrupted = deleteBits(encoded.bits, DELETE_AT_BIT, DELETE_COUNT);
  const result = decodeTolerant(corrupted, book);
  // The sync word originally at byte 100 (bit 800) shifts to bit 797 after losing 3 bits.
  assert.equal(result.syncBitPositions[1], SEGMENT_BITS - DELETE_COUNT);
  assert.equal((result.syncBitPositions[1] + DELETE_COUNT) / 8, SEGMENT_BYTES);
});

test("3-bit deletion: tolerant decoder salvages >= 90% of post-corruption content", () => {
  const corrupted = deleteBits(encoded.bits, DELETE_AT_BIT, DELETE_COUNT);
  const result = decodeTolerant(corrupted, book);
  const { seg1Start, atRisk } = postCorruptionStats();
  const expectedTail = symbols.slice(seg1Start);
  const gotTail = result.symbols.slice(result.symbols.length - expectedTail.length);
  assert.deepEqual(gotTail, expectedTail, "everything from the byte-100 sync word on must be exact");
  const recoveryRate = expectedTail.length / atRisk;
  assert.ok(recoveryRate >= 0.9, `recovery rate ${recoveryRate} below 90%`);
});

test("single-bit flip: damage is isolated to one segment", () => {
  const corrupted = flipBit(encoded.bits, DELETE_AT_BIT);
  const result = decodeTolerant(corrupted, book);
  const { seg1Start } = postCorruptionStats();
  const expectedTail = symbols.slice(seg1Start);
  const gotTail = result.symbols.slice(result.symbols.length - expectedTail.length);
  assert.deepEqual(gotTail, expectedTail);
});

test("corruption inside a later segment still resyncs at the next sync word", () => {
  const atBit = SEGMENT_BITS * 5 + 300; // middle of segment 5
  const corrupted = deleteBits(encoded.bits, atBit, DELETE_COUNT);
  const result = decodeTolerant(corrupted, book);
  const seg6Start = encoded.symbolBitPositions.filter((p) => p < SEGMENT_BITS * 6).length;
  const expectedTail = symbols.slice(seg6Start);
  const gotTail = result.symbols.slice(result.symbols.length - expectedTail.length);
  assert.deepEqual(gotTail, expectedTail);
  assert.ok(result.syncBitPositions.includes(SEGMENT_BITS * 6 - DELETE_COUNT));
});

test("sync word never appears by accident inside a clean stream's data", () => {
  for (let i = 0; i + SYNC_BITS <= encoded.bits.length; i++) {
    if (findSync(encoded.bits, i) !== i) continue;
    const at = findSync(encoded.bits, i);
    assert.ok(encoded.syncBitPositions.includes(at), `unexpected sync word at bit ${at}`);
    i = at; // skip past this sync word
  }
});
