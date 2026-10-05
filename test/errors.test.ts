import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compress, decompress } from '../src/api.ts';
import {
  decodeBlock,
  encodeBlock,
  encodeTokensPayload,
  serializeBlock,
} from '../src/block.ts';
import { BlockError, concatBytes } from '../src/format.ts';
import { StreamDecoder } from '../src/decoder.ts';
import { crc32 } from '../src/crc32.ts';
import type { Token } from '../src/tokens.ts';

function craftBlock(tokens: Token[], origLen: number, windowBits: number, crc = 0): Uint8Array {
  const { payload, mainLens, distLens } = encodeTokensPayload(tokens, windowBits);
  return serializeBlock(origLen, crc, windowBits, payload, mainLens, distLens);
}

function expectCode(code: string): (e: unknown) => boolean {
  return (e: unknown) => e instanceof BlockError && e.code === code;
}

test('truncation at every byte is rejected (one-shot and streaming)', () => {
  const rand = (() => { let s = 1; return () => (s = (s * 1103515245 + 12345) & 0x7fffffff); })();
  const data = new Uint8Array(500).map((_, i) => (i % 11 < 7 ? 0x61 + (i % 26) : rand() % 256));
  const stream = compress(data, { windowBits: 8 });
  for (let i = 0; i < stream.length; i++) {
    const prefix = stream.subarray(0, i);
    assert.throws(() => decodeBlock(prefix), expectCode('TRUNCATED'), `one-shot prefix ${i}`);
    // A zero-byte prefix is a valid empty stream, so the streaming check
    // starts at 1: any nonempty proper prefix must be rejected.
    if (i === 0) continue;
    const dec = new StreamDecoder();
    dec.push(prefix);
    dec.end();
    assert.throws(() => dec.finish(), expectCode('TRUNCATED'), `streaming prefix ${i}`);
  }
  // And the full stream still decodes.
  assert.deepEqual(decompress(stream), data);
});

test('declared original length overflow is rejected before expanding', () => {
  const payload = new Uint8Array([0]);
  const mainLens = new Uint8Array(265);
  mainLens[264] = 1; // only END
  const huge = serializeBlock(0xffffffff, 0, 4, payload, mainLens, new Uint8Array(5));
  assert.throws(() => decodeBlock(huge), expectCode('DECLARED_TOO_LARGE'));
  // A block just above a caller-supplied limit is also rejected.
  const data = new Uint8Array(1000).fill(1);
  const block = encodeBlock(data, 8);
  assert.throws(
    () => decodeBlock(block, 0, { maxBlockSize: 999 }),
    expectCode('DECLARED_TOO_LARGE'),
  );
  assert.deepEqual(decodeBlock(block, 0, { maxBlockSize: 1000 }).data, data);
});

test('expansion beyond the declared length is rejected', () => {
  // Tokens expand to 4 bytes but the header declares 3.
  const tokens: Token[] = [
    { type: 'literal', byte: 0x41 },
    { type: 'match', length: 3, distance: 1 },
  ];
  const block = craftBlock(tokens, 3, 4);
  assert.throws(() => decodeBlock(block), expectCode('OVERFLOW'));
  // Declaring more than the stream expands is also an error.
  const block2 = craftBlock(tokens, 5, 4);
  assert.throws(() => decodeBlock(block2), expectCode('CORRUPT'));
});

test('illegal distances are rejected', () => {
  // Distance beyond produced history.
  const tooFar: Token[] = [
    { type: 'literal', byte: 0x41 },
    { type: 'match', length: 3, distance: 5 },
  ];
  assert.throws(() => decodeBlock(craftBlock(tooFar, 4, 8)), expectCode('BAD_DISTANCE'));
  // Distance beyond the declared window (windowBits=4 -> window 16).
  const beyondWindow: Token[] = [
    ...Array.from({ length: 20 }, () => ({ type: 'literal', byte: 0x42 }) as Token),
    { type: 'match', length: 3, distance: 20 },
  ];
  assert.throws(() => decodeBlock(craftBlock(beyondWindow, 23, 4)), expectCode('BAD_DISTANCE'));
  // Same tokens are legal with a large enough window.
  const expected = new Uint8Array(23).fill(0x42);
  const ok = decodeBlock(craftBlock(beyondWindow, 23, 8, crc32(expected)));
  assert.deepEqual(ok.data, expected);
});

test('oversubscribed code table in a block header is rejected', () => {
  const mainLens = new Uint8Array(265);
  mainLens[0] = 1;
  mainLens[1] = 1;
  mainLens[2] = 1; // Kraft sum 1.5
  const block = serializeBlock(0, 0, 4, new Uint8Array([0]), mainLens, new Uint8Array(5));
  assert.throws(() => decodeBlock(block), expectCode('OVERSUBSCRIBED'));
});

test('crc mismatch is detected', () => {
  const tokens: Token[] = [
    { type: 'literal', byte: 0x41 },
    { type: 'literal', byte: 0x41 },
    { type: 'literal', byte: 0x41 },
  ];
  const good = crc32(new Uint8Array([0x41, 0x41, 0x41]));
  const block = craftBlock(tokens, 3, 4, good ^ 1);
  assert.throws(() => decodeBlock(block), expectCode('CHECKSUM'));
});

test('flipping a payload byte corrupts only its own block', () => {
  const textA = new TextEncoder().encode('alpha '.repeat(200));
  const textB = new TextEncoder().encode('bravo '.repeat(200));
  const blockA = encodeBlock(textA, 8);
  const blockB = encodeBlock(textB, 8);
  const corrupted = Uint8Array.from(blockA);
  corrupted[corrupted.length - 1] ^= 0xff; // inside block A's payload
  const stream = concatBytes([corrupted, blockB]);

  // Block B is independently decodable from its offset.
  assert.deepEqual(decodeBlock(stream, blockA.length).data, textB);

  // Streaming: block A fails, recover() skips it, block B still comes out.
  const dec = new StreamDecoder();
  dec.push(stream);
  assert.throws(() => dec.finish(), (e: unknown) => e instanceof BlockError);
  dec.recover();
  const parts: Uint8Array[] = [];
  for (;;) {
    const chunk = dec.read(1 << 16);
    if (chunk.length === 0) break;
    parts.push(chunk);
  }
  assert.deepEqual(concatBytes(parts), textB);
});

test('bad magic and absurd header fields are rejected', () => {
  const block = Uint8Array.from(encodeBlock(new Uint8Array([1, 2, 3]), 8));
  const badMagic = Uint8Array.from(block);
  badMagic[0] = 0x00;
  assert.throws(() => decodeBlock(badMagic), expectCode('BAD_FORMAT'));
  const badWindow = Uint8Array.from(block);
  badWindow[3] = 99;
  assert.throws(() => decodeBlock(badWindow), expectCode('BAD_FORMAT'));
  const badPayloadLen = Uint8Array.from(block);
  badPayloadLen[8] = 0xff;
  badPayloadLen[9] = 0xff;
  badPayloadLen[10] = 0xff;
  badPayloadLen[11] = 0x7f;
  assert.throws(() => decodeBlock(badPayloadLen), expectCode('BAD_FORMAT'));
});
