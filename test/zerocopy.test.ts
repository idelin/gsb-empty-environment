import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encode, decode, WireReader, WireWriter } from '../src/index.ts';
import type { Schema } from '../src/index.ts';

const Doc: Schema = [
  { no: 1, name: 'title', type: 'string' },
  { no: 2, name: 'body', type: 'bytes' },
];
const Envelope: Schema = [
  { no: 1, name: 'doc', type: 'message', schema: Doc },
  { no: 2, name: 'checksum', type: 'fixed64' },
];

test('bytes field is a zero-copy view into the source buffer', () => {
  const src = { title: 't', body: new Uint8Array([10, 20, 30]) };
  const persisted = encode(Doc, src);
  const out = decode(Doc, persisted);
  const body = out.body as Uint8Array;
  assert.equal(body.buffer, persisted.buffer);       // 同一块内存，无复制
  assert.equal(body.byteOffset, persisted.byteOffset + persisted.length - 3);
  assert.deepEqual([...body], [10, 20, 30]);
});

test('nested message fields are zero-copy views of the mapped buffer', () => {
  const persisted = encode(Envelope, {
    doc: { title: 'hello', body: new Uint8Array([1, 2, 3, 4]) },
    checksum: 0x1122_3344_5566_7788n,
  });
  const out = decode(Envelope, persisted);
  const doc = out.doc as Record<string, unknown>;
  assert.equal((doc.body as Uint8Array).buffer, persisted.buffer);
  assert.equal(doc.title, 'hello');
  assert.equal(out.checksum, 0x1122_3344_5566_7788n);
});

test('reader over a memory-mapped style window does not copy', () => {
  const mapped = new Uint8Array(4096);               // 模拟 mmap 页
  const payload = encode(Doc, { title: 'x', body: new Uint8Array([7, 8]) });
  mapped.set(payload, 1000);
  const r = new WireReader(mapped, 1000, 1000 + payload.length);
  const out = decode(Doc, mapped.subarray(1000, 1000 + payload.length));
  assert.equal((out.body as Uint8Array).buffer, mapped.buffer);
  assert.equal(r.remaining, payload.length);
});

test('decode never mutates the input buffer', () => {
  const persisted = encode(Doc, { title: 'immutable', body: new Uint8Array([1, 2, 3]) });
  const snapshot = persisted.slice();
  decode(Doc, persisted);
  assert.deepEqual(persisted, snapshot);
});

test('writer output is exact-length and independent of internal capacity', () => {
  const w = new WireWriter();
  for (let i = 0; i < 500; i++) w.varint(i * 7);
  const a = w.finish();
  const b = w.finish();
  assert.equal(a.buffer.byteLength, a.length);
  assert.notEqual(a, b);
  assert.deepEqual(a, b);
});

test('concurrent encode/decode is safe: pure functions, no shared state', async () => {
  const src = { title: '并发', body: new Uint8Array([9, 9, 9]) };
  const expected = encode(Doc, src);
  const jobs = Array.from({ length: 256 }, async (_, i) => {
    await Promise.resolve();
    const bytes = encode(Doc, { title: `并发-${i}`, body: new Uint8Array([i & 0xff]) });
    const out = decode(Doc, bytes);
    assert.equal(out.title, `并发-${i}`);
    assert.deepEqual([...(out.body as Uint8Array)], [i & 0xff]);
    return decode(Doc, expected);
  });
  const results = await Promise.all(jobs);
  for (const out of results) {
    assert.equal(out.title, '并发');
    assert.deepEqual([...(out.body as Uint8Array)], [9, 9, 9]);
  }
});

test('independent readers over one shared buffer do not interfere', () => {
  const buf = encode(Doc, { title: 'shared', body: new Uint8Array([5, 6, 7, 8]) });
  const r1 = new WireReader(buf);
  const r2 = new WireReader(buf);
  r1.tag(); r1.string();                             // r1 读到底
  assert.equal(r2.remaining, buf.length);            // r2 不受影响
  r2.tag();
  assert.equal(r2.string(), 'shared');
});
