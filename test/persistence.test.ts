import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { decodeMessage, encodeMessage } from '../src/message.ts';
import { addressBookSchema, sampleBook } from './helpers/fixture.ts';

test('persist to disk and restore zero-copy from the file buffer', () => {
  const dir = mkdtempSync(join(tmpdir(), 'pbcodec-'));
  try {
    const book = sampleBook();
    const encoded = encodeMessage(addressBookSchema, book);
    const file = join(dir, 'addressbook.pb');
    writeFileSync(file, encoded);

    const fileBuffer = readFileSync(file); // memory-mapped style: parse directly over the file bytes
    const view = new Uint8Array(fileBuffer.buffer, fileBuffer.byteOffset, fileBuffer.byteLength);
    const decoded = decodeMessage(addressBookSchema, view);

    const expectedPeople = book.people.map((p) => ({ email: '', ...p }));
    assert.deepEqual(decoded.people, expectedPeople);
    assert.equal(decoded.version, book.version);

    const people = decoded.people as Array<Record<string, unknown>>;
    const avatar = people[0].avatar as Uint8Array;
    assert.strictEqual(
      avatar.buffer,
      fileBuffer.buffer,
      'restored bytes field must alias the file buffer (zero-copy)',
    );
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test('append-only log: multiple messages framed by length prefix round-trip', () => {
  const dir = mkdtempSync(join(tmpdir(), 'pbcodec-'));
  try {
    const file = join(dir, 'log.pb');
    const frames: Uint8Array[] = [];
    const books = [sampleBook(), { people: [], version: 9 }, sampleBook()];
    let total = 0;
    for (const b of books) {
      const body = encodeMessage(addressBookSchema, b);
      const head = encodeFrameLength(body.length);
      frames.push(head, body);
      total += head.length + body.length;
    }
    const blob = new Uint8Array(total);
    let at = 0;
    for (const f of frames) {
      blob.set(f, at);
      at += f.length;
    }
    writeFileSync(file, blob);

    const back = readFileSync(file);
    const view = new Uint8Array(back.buffer, back.byteOffset, back.byteLength);
    const restored = [];
    let pos = 0;
    while (pos < view.length) {
      const { value, offset } = readFrameLength(view, pos);
      pos = offset;
      restored.push(decodeMessage(addressBookSchema, view.subarray(pos, pos + value)));
      pos += value;
    }
    assert.equal(restored.length, 3);
    const expectedPeople = (b: unknown) =>
      ((b as Record<string, unknown>).people as Array<Record<string, unknown>>).map((p) => ({ email: '', ...p }));
    assert.deepEqual(restored[0].people, expectedPeople(books[0]));
    assert.deepEqual(restored[1], { people: [], version: 9 });
    assert.deepEqual(restored[2].people, expectedPeople(books[2]));
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

function encodeFrameLength(n: number): Uint8Array {
  const out: number[] = [];
  let v = n >>> 0;
  while (v > 0x7f) {
    out.push((v & 0x7f) | 0x80);
    v >>>= 7;
  }
  out.push(v);
  return new Uint8Array(out);
}

function readFrameLength(buf: Uint8Array, pos: number): { value: number; offset: number } {
  let result = 0;
  let shift = 0;
  let p = pos;
  for (;;) {
    const b = buf[p];
    p += 1;
    result |= (b & 0x7f) << shift;
    if ((b & 0x80) === 0) return { value: result >>> 0, offset: p };
    shift += 7;
  }
}
