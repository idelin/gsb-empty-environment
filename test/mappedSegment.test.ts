import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { encodeFrame } from '../src/codec/frame.ts';
import { MappedSegment } from '../src/storage/mappedSegment.ts';
import { resources } from '../src/storage/resources.ts';

test('mapped segment scans frames lazily with zero-copy views', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'seg-'));
  try {
    const frames = [encodeFrame(Buffer.from('aaa')), encodeFrame(Buffer.from('bbbb')), encodeFrame(Buffer.from('ccccc'))];
    const file = join(dir, 's.seg');
    await writeFile(file, Buffer.concat(frames));

    const seg = MappedSegment.map(file);
    const seen: string[] = [];
    for (const f of seg.scan()) {
      assert.equal(f.payload.buffer, seg.buffer.buffer); // views into the mapping
      seen.push(f.payload.toString());
    }
    assert.deepEqual(seen, ['aaa', 'bbbb', 'ccccc']);
    assert.equal(seg.truncatedAt, -1);
    seg.close();
    assert.equal(resources.outstanding('segment'), 0);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('mapped segment tolerates a torn tail and reports the offset', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'seg-'));
  try {
    const good1 = encodeFrame(Buffer.from('first'));
    const good2 = encodeFrame(Buffer.from('second'));
    const torn = encodeFrame(Buffer.from('crashed-mid-write')).subarray(0, 9);
    const file = join(dir, 's.seg');
    await writeFile(file, Buffer.concat([good1, good2, torn]));

    const seg = MappedSegment.map(file);
    const seen = [...seg.scan()].map((f) => f.payload.toString());
    assert.deepEqual(seen, ['first', 'second']);
    assert.equal(seg.truncatedAt, good1.length + good2.length);
    // random access still works for intact frames
    assert.equal(seg.at(good1.length).payload.toString(), 'second');
    seg.close();
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('mapped segment rejects use after close', async () => {
  const seg = MappedSegment.fromBuffer(encodeFrame(Buffer.from('x')));
  seg.close();
  assert.throws(() => [...seg.scan()], /closed/);
  assert.throws(() => seg.at(0), /closed/);
  seg.close(); // idempotent
  assert.equal(resources.outstanding('segment'), 0);
});

test('empty segment scans to nothing', () => {
  const seg = MappedSegment.fromBuffer(Buffer.alloc(0));
  assert.equal([...seg.scan()].length, 0);
  assert.equal(seg.truncatedAt, -1);
  seg.close();
});
