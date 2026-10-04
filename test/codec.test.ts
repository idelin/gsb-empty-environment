import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Buffer } from 'node:buffer';
import {
  CodecError,
  EntryType,
  FIXED_HEADER_LEN,
  FORMAT_VERSION,
  FrameReader,
  MAX_PAYLOAD_LEN,
  decodeEntryView,
  encodeEntry,
  encodedSize,
  scanFrames,
} from '../src/codec.ts';
import { crc32 } from '../src/crc32.ts';

function hasCode(code: string) {
  return (e: unknown) => e instanceof CodecError && (e as CodecError).code === code;
}

test('round-trip: fields survive encode/decode', () => {
  const payload = Buffer.from('hello paxos');
  const frame = encodeEntry({ term: 7n, index: 42n, type: EntryType.Data, payload });
  assert.equal(frame.length, encodedSize(payload.length));
  const view = decodeEntryView(frame);
  assert.equal(view.version, FORMAT_VERSION);
  assert.equal(view.type, EntryType.Data);
  assert.equal(view.term, 7n);
  assert.equal(view.index, 42n);
  assert.deepEqual(view.payload, payload);
  assert.equal(view.frameLen, frame.length);
  assert.equal(view.headerLen, FIXED_HEADER_LEN);
});

test('round-trip: u64 boundary values (max safe and beyond 2^53)', () => {
  const big = 2n ** 64n - 1n;
  const frame = encodeEntry({ term: big, index: 2n ** 53n + 1n, payload: Buffer.alloc(0) });
  const view = decodeEntryView(frame);
  assert.equal(view.term, big);
  assert.equal(view.index, 2n ** 53n + 1n);
});

test('zero-copy: payload/extensions are views onto the source buffer', () => {
  const payload = Buffer.from('shared-memory');
  const frame = encodeEntry({ term: 1n, index: 1n, payload, extensions: Buffer.from([1, 2, 3, 4]) });
  const view = decodeEntryView(frame);
  assert.strictEqual(view.payload.buffer, frame.buffer);
  assert.strictEqual(view.extensions.buffer, frame.buffer);
  // Mutating the source frame is visible through the view (no copy happened).
  const off = view.payload.byteOffset - frame.byteOffset;
  frame.writeUInt8(0x58, off);
  assert.equal(view.payload[0], 0x58);
});

test('empty payload and empty extensions', () => {
  const view = decodeEntryView(encodeEntry({ term: 0n, index: 0n }));
  assert.equal(view.payload.length, 0);
  assert.equal(view.extensions.length, 0);
  assert.equal(view.frameLen, FIXED_HEADER_LEN);
});

test('truncated buffer at every cut point throws TRUNCATED', () => {
  const frame = encodeEntry({ term: 3n, index: 9n, payload: Buffer.from('boundary') });
  for (let cut = 0; cut < frame.length; cut++) {
    assert.throws(() => decodeEntryView(frame.subarray(0, cut)), (e: unknown) => {
      assert.ok(e instanceof CodecError);
      assert.equal((e as CodecError).code, 'TRUNCATED');
      return true;
    });
  }
  // And the exact length always decodes.
  assert.equal(decodeEntryView(frame).index, 9n);
});

test('bad magic is rejected', () => {
  const frame = encodeEntry({ term: 1n, index: 1n });
  frame.writeUInt32LE(0xdeadbeef, 0);
  assert.throws(() => decodeEntryView(frame), hasCode('BAD_MAGIC'));
});

test('crc mismatch on any single-bit corruption', () => {
  for (const pos of [8, 20, FIXED_HEADER_LEN - 1, FIXED_HEADER_LEN, FIXED_HEADER_LEN + 2]) {
    const frame = encodeEntry({ term: 5n, index: 6n, payload: Buffer.from('integrity-check') });
    frame[pos] ^= 0x01;
    assert.throws(() => decodeEntryView(frame), (e: unknown) => {
      const code = (e as CodecError).code;
      assert.ok(code === 'CRC_MISMATCH' || code === 'TRUNCATED', `pos ${pos} got ${code}`);
      return true;
    });
  }
});

test('overflow: absurd payloadLen is rejected before any allocation', () => {
  const frame = encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('x') });
  frame.writeUInt32LE(0xffffffff, 24); // payloadLen = 4GiB-1
  // Recompute crc so only the length check can fire.
  frame.writeUInt32LE(0, 28);
  frame.writeUInt32LE(crc32(frame), 28);
  assert.throws(() => decodeEntryView(frame), (e: unknown) => {
    assert.equal((e as CodecError).code, 'OVERFLOW');
    return true;
  });
});

test('overflow: payloadLen within hard limit but past buffer end is TRUNCATED', () => {
  const frame = encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('x') });
  frame.writeUInt32LE(1024, 24);
  frame.writeUInt32LE(0, 28);
  frame.writeUInt32LE(crc32(frame), 28);
  assert.throws(() => decodeEntryView(frame), (e: unknown) => {
    assert.equal((e as CodecError).code, 'TRUNCATED');
    return true;
  });
});

test('custom maxPayloadLen boundary: exactly at limit ok, limit+1 overflows', () => {
  const payload = Buffer.alloc(16, 0x61);
  const frame = encodeEntry({ term: 1n, index: 1n, payload });
  assert.equal(decodeEntryView(frame, 0, { maxPayloadLen: 16 }).payload.length, 16);
  assert.throws(() => decodeEntryView(frame, 0, { maxPayloadLen: 15 }), hasCode('OVERFLOW'));
});

test('encode rejects payload above MAX_PAYLOAD_LEN', () => {
  const huge = { length: MAX_PAYLOAD_LEN + 1 } as Uint8Array;
  assert.throws(() => encodeEntry({ term: 1n, index: 1n, payload: huge }), hasCode('PAYLOAD_TOO_LARGE'));
});

test('backward compat: current reader decodes older-version frames', () => {
  const frame = encodeEntry({ term: 2n, index: 3n, version: 0, payload: Buffer.from('old') });
  const view = decodeEntryView(frame, 0, { maxVersion: FORMAT_VERSION });
  assert.equal(view.version, 0);
  assert.deepEqual(view.payload, Buffer.from('old'));
});

test('forward compat: old reader skips unknown header extensions via headerLen', () => {
  const ext = Buffer.from('future-field');
  const frame = encodeEntry({
    term: 9n,
    index: 9n,
    version: FORMAT_VERSION + 1,
    extensions: ext,
    payload: Buffer.from('new-payload'),
  });
  // A reader that does not know the new version still parses the frame.
  const view = decodeEntryView(frame);
  assert.equal(view.version, FORMAT_VERSION + 1);
  assert.deepEqual(view.extensions, ext);
  assert.deepEqual(view.payload, Buffer.from('new-payload'));
  // A strict old reader may refuse by version policy.
  assert.throws(() => decodeEntryView(frame, 0, { maxVersion: FORMAT_VERSION }), hasCode('UNSUPPORTED_VERSION'));
});

test('headerLen smaller than fixed header is rejected', () => {
  const frame = encodeEntry({ term: 1n, index: 1n });
  frame.writeUInt16LE(FIXED_HEADER_LEN - 1, 6);
  frame.writeUInt32LE(0, 28);
  frame.writeUInt32LE(crc32(frame), 28);
  assert.throws(() => decodeEntryView(frame), hasCode('BAD_HEADER'));
});

test('decode at non-zero offset inside a larger mapped buffer', () => {
  const a = encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('AA') });
  const b = encodeEntry({ term: 2n, index: 2n, payload: Buffer.from('BBBB') });
  const image = Buffer.concat([a, b]);
  const va = decodeEntryView(image, 0);
  const vb = decodeEntryView(image, a.length);
  assert.equal(va.index, 1n);
  assert.equal(vb.index, 2n);
  assert.equal(vb.offset, a.length);
  assert.strictEqual(vb.payload.buffer, image.buffer);
});

test('scanFrames iterates a whole segment with exact offsets', () => {
  const frames = [
    encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('one') }),
    encodeEntry({ term: 1n, index: 2n, payload: Buffer.from('two!!') }),
    encodeEntry({ term: 2n, index: 3n }),
  ];
  const image = Buffer.concat(frames);
  const views = [...scanFrames(image)];
  assert.equal(views.length, 3);
  assert.deepEqual(views.map((v) => Number(v.index)), [1, 2, 3]);
  let off = 0;
  for (let i = 0; i < 3; i++) {
    assert.equal(views[i].offset, off);
    off += frames[i].length;
  }
  // Trailing garbage after valid frames is a hard error, not silent data loss.
  const corrupt = Buffer.concat([image, Buffer.from('junk')]);
  assert.throws(
    () => [...scanFrames(corrupt)],
    (e: unknown) => e instanceof CodecError && ['TRUNCATED', 'BAD_MAGIC'].includes((e as CodecError).code),
  );
});

test('FrameReader reassembles frames split across arbitrary chunk boundaries', () => {
  const frames = [
    encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('split-me') }),
    encodeEntry({ term: 2n, index: 2n, payload: Buffer.alloc(100, 0x62) }),
    encodeEntry({ term: 3n, index: 3n }),
  ];
  const stream = Buffer.concat(frames);
  for (const chunkSize of [1, 3, 7, FIXED_HEADER_LEN, stream.length]) {
    const reader = new FrameReader();
    const out = [];
    for (let i = 0; i < stream.length; i += chunkSize) {
      out.push(...reader.feed(stream.subarray(i, Math.min(i + chunkSize, stream.length))));
    }
    assert.equal(out.length, 3, `chunkSize ${chunkSize}`);
    assert.deepEqual(out.map((v) => Number(v.index)), [1, 2, 3]);
    assert.equal(reader.bufferedBytes, 0);
  }
});

test('FrameReader rejects corrupt stream and resets', () => {
  const reader = new FrameReader();
  const bad = encodeEntry({ term: 1n, index: 1n, payload: Buffer.from('data') });
  bad[bad.length - 1] ^= 0xff;
  assert.throws(() => reader.feed(bad), hasCode('CRC_MISMATCH'));
  assert.equal(reader.bufferedBytes, 0);
});
