import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalUtf8Decoder,
  Utf8DecodeError,
  type DecodeEvent,
  type Strategy,
} from '../src/index.ts';

// Independent whole-buffer reference decoders.
const refReplace = new TextDecoder('utf-8', { fatal: false });
const refStrict = new TextDecoder('utf-8', { fatal: true });

/** All compositions of n: every possible way to cut n bytes into chunks. */
function chunkings(n: number): number[][] {
  const out: number[][] = [];
  const rec = (rest: number, acc: number[]): void => {
    if (rest === 0) {
      out.push(acc);
      return;
    }
    for (let s = 1; s <= rest; s++) rec(rest - s, [...acc, s]);
  };
  rec(n, []);
  return out;
}

function decodeWith(bytes: Uint8Array, sizes: number[], strategy: Strategy): DecodeEvent[] {
  const d = new IncrementalUtf8Decoder(strategy);
  const events: DecodeEvent[] = [];
  let pos = 0;
  for (const s of sizes) {
    events.push(...d.push(bytes.subarray(pos, pos + s)));
    pos += s;
  }
  events.push(...d.finish());
  return events;
}

const textOf = (events: DecodeEvent[]): string => events.map((e) => e.text).join('');

const shape = (events: DecodeEvent[]): unknown =>
  events.map((e) => [e.kind, e.byteStart, e.byteEnd, e.u16Start, e.u16Length, e.text]);

/**
 * Check one input under every chunking, in both strategies, against the
 * independent whole-buffer TextDecoder, and verify chunk-boundary invariance.
 */
function checkBytes(bytes: Uint8Array, label: string): void {
  const expectedReplace = refReplace.decode(bytes);
  let expectedStrictOk = true;
  let expectedStrictText = '';
  try {
    expectedStrictText = refStrict.decode(bytes);
  } catch {
    expectedStrictOk = false;
  }

  const allEvents: DecodeEvent[][] = [];
  for (const sizes of chunkings(bytes.length)) {
    // replace strategy must match the independent decoder exactly.
    const events = decodeWith(bytes, sizes, 'replace');
    assert.equal(
      textOf(events),
      expectedReplace,
      `${label}: replace mismatch for [${[...bytes]}] chunks [${sizes}]`,
    );

    // strict strategy must agree with the fatal decoder on error-or-not.
    let strictOk = true;
    let strictText = '';
    let strictError: Utf8DecodeError | undefined;
    try {
      strictText = textOf(decodeWith(bytes, sizes, 'strict'));
    } catch (e) {
      assert.ok(e instanceof Utf8DecodeError, `${label}: unexpected error type`);
      strictOk = false;
      strictError = e;
    }
    assert.equal(strictOk, expectedStrictOk, `${label}: strict ok mismatch for [${[...bytes]}]`);
    if (strictOk) {
      assert.equal(strictText, expectedStrictText);
    } else {
      // The strict error must be absolute and coincide with the first
      // replacement emitted by the replace strategy.
      const firstReplacement = events.find((e) => e.kind === 'replacement');
      assert.ok(firstReplacement !== undefined);
      assert.equal(strictError!.byteOffset, firstReplacement.byteStart, `${label}: error position`);
      assert.ok(strictError!.byteOffset >= 0 && strictError!.byteOffset < Math.max(bytes.length, 1));
    }

    // Events must tile the input and stay monotonic in both spaces.
    let bytePos = 0;
    let u16Pos = 0;
    for (const e of events) {
      assert.equal(e.byteStart, bytePos, `${label}: byte contiguity`);
      assert.equal(e.u16Start, u16Pos, `${label}: u16 contiguity`);
      assert.ok(e.byteEnd > e.byteStart);
      assert.ok(e.u16Length === 1 || e.u16Length === 2);
      assert.equal(e.text.length, e.u16Length);
      bytePos = e.byteEnd;
      u16Pos += e.u16Length;
    }
    assert.equal(bytePos, bytes.length, `${label}: events cover all bytes`);
    allEvents.push(events);
  }

  // Chunk boundaries must not change the result.
  const first = JSON.stringify(shape(allEvents[0]));
  for (const events of allEvents) {
    assert.equal(JSON.stringify(shape(events)), first, `${label}: chunk-boundary invariance`);
  }
}

test('exhaustive: every 1-byte and 2-byte input, every chunking, both strategies', () => {
  const bytes = new Uint8Array(2);
  for (let n = 1; n <= 2; n++) {
    const total = 1 << (8 * n);
    for (let v = 0; v < total; v++) {
      for (let i = 0; i < n; i++) bytes[i] = (v >>> (8 * i)) & 0xff;
      checkBytes(bytes.subarray(0, n), `len${n}#${v}`);
    }
  }
});

const curated: number[][] = [
  [0xe0, 0xa0, 0x80], // U+0800 (smallest 3-byte)
  [0xe0, 0x80, 0x80], // overlong 3-byte
  [0xed, 0x9f, 0xbf], // U+D7FF (below surrogates)
  [0xed, 0xa0, 0x80], // U+D800 surrogate (invalid)
  [0xed, 0xbf, 0xbf], // U+DFFF surrogate (invalid)
  [0xef, 0xbf, 0xbf], // U+FFFF
  [0xe2, 0x82, 0xac], // euro sign
  [0xf0, 0x90, 0x80, 0x80], // U+10000 (smallest 4-byte)
  [0xf0, 0x9f, 0x98, 0x80], // U+1F600 emoji
  [0xf0, 0x80, 0x80, 0x80], // overlong 4-byte
  [0xf0, 0x8f, 0xbf, 0xbf], // overlong 4-byte
  [0xf4, 0x8f, 0xbf, 0xbf], // U+10FFFF (largest valid)
  [0xf4, 0x90, 0x80, 0x80], // above U+10FFFF
  [0xf5, 0x80, 0x80, 0x80], // invalid lead
  [0x61, 0xe2, 0x82, 0x62], // ASCII, truncated 3-byte, ASCII
  [0xe2, 0x28, 0xa1, 0x62], // bad continuation then ASCII
  [0xc3, 0xa9, 0xf0, 0x9f], // é + truncated emoji
  [0xf0, 0x9f, 0x98, 0xff], // emoji prefix + invalid byte
  [0x80, 0x80, 0x80, 0x80], // four stray continuations
  [0xbf, 0xef, 0xbf, 0xbd], // stray + valid U+FFFD
  [0xc0, 0xaf, 0xe0, 0x81], // overlong 2-byte + bad 3-byte
  [0x41, 0xf4, 0x8f, 0xbf], // ASCII + truncated U+10FFFF
];

test('exhaustive chunkings and every truncation prefix for curated 3-4 byte inputs', () => {
  for (const arr of curated) {
    const full = Uint8Array.from(arr);
    for (let n = 0; n <= full.length; n++) {
      checkBytes(full.subarray(0, n), `curated[${arr.map((b) => b.toString(16))}]prefix${n}`);
    }
  }
});

test('deterministic fuzz: random 3-4 byte inputs, every chunking', () => {
  let state = 0x12345678;
  const rand = (): number => {
    state = (state * 1103515245 + 12345) & 0x7fffffff;
    return state;
  };
  for (let iter = 0; iter < 3000; iter++) {
    const n = 3 + (rand() % 2);
    const bytes = new Uint8Array(n);
    for (let i = 0; i < n; i++) bytes[i] = rand() & 0xff;
    checkBytes(bytes, `fuzz#${iter}`);
  }
});
