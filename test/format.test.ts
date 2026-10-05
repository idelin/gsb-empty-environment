import * as fs from 'fs';
import {
  BLOCK_SIZE,
  DIR_ENTRY_SIZE,
  FormatError,
  HEADER_SIZE,
  decodeFragment,
  encodeFragment,
  encodeVarint,
  decodeVarint,
} from '../src/format';
import { loadSetIds, validateFile } from '../src/validate';
import { assert, run, test } from './harness';
import { randomUniqueIds, range, tmpDir, writeSet, writeSetWithShards } from './helpers';

const U32_MAX = 0xffffffff;

test('varint roundtrips uint32 edge values', () => {
  for (const v of [0, 1, 127, 128, 300, 16384, 2147483648, U32_MAX]) {
    const enc = Buffer.from(encodeVarint(v));
    const dec = decodeVarint(enc, 0);
    assert.equal(dec.value, v);
    assert.equal(dec.next, enc.length);
  }
});

test('fragment roundtrip with huge sparse gaps', () => {
  const ids = [0, 1, 2147483648, 2147483649, U32_MAX - 1, U32_MAX];
  const frag = encodeFragment(ids);
  const blocks = decodeFragment(frag);
  assert.deepEqual([...blocks.flatMap((b) => [...b.ids])], ids);
});

test('build + validate + load roundtrip over many shards', () => {
  const dir = tmpDir();
  const ids = randomUniqueIds(5000, 100000, 42);
  const file = writeSet(dir, 'A', 'v1', ids, 333);
  const v = validateFile(file);
  assert.deepEqual(v.errors, []);
  assert.equal(v.ok, true);
  assert.equal(v.setId, 'A');
  assert.equal(v.version, 'v1');
  assert.equal(v.totalIds, 5000);
  assert.deepEqual(loadSetIds(file), ids);
});

test('sparse ids at uint32 extremes validate and reload', () => {
  const dir = tmpDir();
  const ids = [0, 1, 2147483648, U32_MAX - 1, U32_MAX];
  const file = writeSet(dir, 'S', 'v1', ids, 2);
  const v = validateFile(file);
  assert.deepEqual(v.errors, []);
  assert.equal(v.ok, true);
  assert.deepEqual(loadSetIds(file), ids);
});

test('empty shards are legal and validate cleanly', () => {
  const dir = tmpDir();
  const file = writeSetWithShards(dir, 'E', 'v1', [
    { rangeStart: 0, rangeEnd: 100, ids: range(0, 100, 2) },
    { rangeStart: 100, rangeEnd: 200, ids: [] },
    { rangeStart: 200, rangeEnd: 300, ids: range(200, 300, 2) },
  ]);
  const v = validateFile(file);
  assert.deepEqual(v.errors, []);
  assert.equal(v.ok, true);
  assert.deepEqual(loadSetIds(file), [...range(0, 100, 2), ...range(200, 300, 2)]);
});

test('unsorted or duplicate ids are rejected at build time', () => {
  const dir = tmpDir();
  assert.throws(() => writeSet(dir, 'B', 'v1', [5, 3, 9], 10), FormatError);
  assert.throws(() => writeSet(dir, 'B', 'v1', [5, 5, 9], 10), FormatError);
  assert.throws(() => writeSet(dir, 'B', 'v1', [-1], 10), FormatError);
  assert.throws(() => writeSet(dir, 'B', 'v1', [U32_MAX + 1], 10), FormatError);
});

test('validator detects a corrupted per-shard count', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'C', 'v1', range(0, 500), 100);
  const buf = fs.readFileSync(file);
  const countOffset = HEADER_SIZE + 12; // shard 0, count field
  buf.writeUInt32LE(buf.readUInt32LE(countOffset) + 7, countOffset);
  fs.writeFileSync(file, buf);
  const v = validateFile(file);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('count mismatch')), 'expected count mismatch, got: ' + v.errors.join('; '));
});

test('validator detects swapped fragments via digest mismatch', () => {
  const dir = tmpDir();
  const file = writeSet(dir, 'D', 'v1', range(0, 400), 100);
  const buf = fs.readFileSync(file);
  const dataStart = HEADER_SIZE + 4 * DIR_ENTRY_SIZE;
  const frag0Len = buf.readUInt32LE(HEADER_SIZE + 20);
  const frag1Offset = buf.readUInt32LE(HEADER_SIZE + DIR_ENTRY_SIZE + 16);
  const frag1Len = buf.readUInt32LE(HEADER_SIZE + DIR_ENTRY_SIZE + 20);
  const frag0 = buf.slice(dataStart, dataStart + frag0Len);
  const frag1 = buf.slice(frag1Offset, frag1Offset + frag1Len);
  const swapped = Buffer.concat([buf.slice(0, dataStart), frag1, frag0]);
  fs.writeFileSync(file, swapped);
  const v = validateFile(file);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('digest mismatch')), 'expected digest mismatch, got: ' + v.errors.join('; '));
});

test('validator detects overlapping shard ranges', () => {
  const dir = tmpDir();
  const file = writeSetWithShards(dir, 'O', 'v1', [
    { rangeStart: 0, rangeEnd: 100, ids: range(0, 100) },
    { rangeStart: 50, rangeEnd: 150, ids: range(100, 150) },
  ]);
  const v = validateFile(file);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('overlaps')), 'expected overlap error, got: ' + v.errors.join('; '));
});

test('validator detects ids outside their shard range', () => {
  const dir = tmpDir();
  const file = writeSetWithShards(dir, 'R', 'v1', [
    { rangeStart: 0, rangeEnd: 50, ids: [10, 20, 80] },
  ]);
  const v = validateFile(file);
  assert.equal(v.ok, false);
  assert.ok(v.errors.some((e) => e.includes('outside shard range')), 'got: ' + v.errors.join('; '));
});

test('block size constant keeps decoded blocks small', () => {
  assert.equal(BLOCK_SIZE, 128);
  const frag = encodeFragment(range(0, 1000));
  const blocks = decodeFragment(frag);
  assert.equal(blocks.length, 8);
  assert.equal(blocks[0].byteLength, 512);
  assert.equal(blocks[7].ids.length, 104);
});

run();
