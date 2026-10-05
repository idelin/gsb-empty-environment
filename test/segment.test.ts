import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';

import { HEADER_SIZE, MAX_DOC_ID, decodeHeader, parseDictionary } from '../src/format.ts';
import { SegmentReader } from '../src/reader.ts';
import { validateSegmentBuffer, type ValidationIssue } from '../src/validator.ts';
import { buildSegmentBuffer, writeSegment } from '../src/writer.ts';

const dir = mkdtempSync(join(tmpdir(), 'cidx-'));
let fileSeq = 0;

function writeTmp(buf: Buffer): string {
  const path = join(dir, `seg-${fileSeq++}.bin`);
  writeFileSync(path, buf);
  return path;
}

function codes(issues: ValidationIssue[]): string[] {
  return issues.map((i) => i.code);
}

function rawIntersect(lists: number[][]): number[] {
  if (lists.length === 0) return [];
  const sets = lists.map((l) => new Set(l));
  const out: number[] = [];
  for (const id of lists[0]) {
    if (sets.every((s) => s.has(id))) out.push(id);
  }
  return out.sort((a, b) => a - b);
}

// Deterministic PRNG (mulberry32) for reproducible random tests.
function rng(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('roundtrip: 写入/校验/单词查询与原始数据一致，重复ID被合并', async () => {
  const input: [string, number[]][] = [
    ['apple', [5, 3, 5, 1, 3, 9]],
    ['banana', [2, 4, 6, 8]],
    ['cherry', [100]],
    ['empty-list', []],
  ];
  const path = join(dir, 'roundtrip.bin');
  const stats = await writeSegment(input, path);
  assert.equal(stats.termCount, 4);

  const reader = await SegmentReader.open(path);
  assert.deepEqual(reader.get('apple'), [1, 3, 5, 9]);
  assert.deepEqual(reader.get('banana'), [2, 4, 6, 8]);
  assert.deepEqual(reader.get('cherry'), [100]);
  assert.deepEqual(reader.get('empty-list'), []);
  assert.deepEqual(reader.get('missing'), []);
  assert.deepEqual(reader.terms().sort(), ['apple', 'banana', 'cherry', 'empty-list']);
});

test('空词项可以写入、校验与查询', async () => {
  const path = join(dir, 'empty-term.bin');
  await writeSegment(
    [
      ['', [1, 2, 3]],
      ['x', [2, 3, 4]],
    ],
    path,
  );
  const reader = await SegmentReader.open(path);
  assert.deepEqual(reader.get(''), [1, 2, 3]);
  assert.deepEqual(reader.intersect(['', 'x']), [2, 3]);
});

test('最大合法文档ID (2^32-1) 往返一致', async () => {
  const path = join(dir, 'max-id.bin');
  const ids = [0, 1, MAX_DOC_ID - 1, MAX_DOC_ID];
  await writeSegment([['max', ids]], path);
  const reader = await SegmentReader.open(path);
  assert.deepEqual(reader.get('max'), ids);
  const cursor = reader.cursor('max');
  cursor.seek(MAX_DOC_ID);
  assert.equal(cursor.done, false);
  assert.equal(cursor.value, MAX_DOC_ID);
  cursor.next();
  assert.equal(cursor.done, true);
});

test('跨块交集：结果与原始集合运算一致，且seek只解码极少数块', async () => {
  const dense: number[] = [];
  for (let i = 0; i < 10000; i++) dense.push(i);
  const sparse = [5, 5005, 9995];
  const path = join(dir, 'cross-block.bin');
  const stats = await writeSegment(
    [
      ['dense', dense],
      ['sparse', sparse],
    ],
    path,
  ); // 默认块大小128 -> dense 共79块
  assert.equal(stats.blockCount, 79 + 1);

  const reader = await SegmentReader.open(path);
  reader.resetStats();
  const result = reader.intersect(['dense', 'sparse']);
  assert.deepEqual(result, rawIntersect([dense, sparse]));
  // sparse 1块 + dense 命中的3块；远少于全部80块，证明跳转表生效。
  assert.ok(
    reader.stats.decodedBlocks <= 5,
    `expected skip-table seeks to decode few blocks, decoded ${reader.stats.decodedBlocks}`,
  );

  reader.resetStats();
  assert.deepEqual(reader.get('dense'), dense);
  assert.equal(reader.stats.decodedBlocks, 79); // 全量读取才解码所有块
});

test('随机数据：多词AND与原始集合运算逐一核对', async () => {
  const rand = rng(42);
  const terms = new Map<string, number[]>();
  for (let t = 0; t < 20; t++) {
    const ids = new Set<number>();
    const n = Math.floor(rand() * 800);
    for (let i = 0; i < n; i++) ids.add(Math.floor(rand() * 5000));
    terms.set(`t${t}`, [...ids]);
  }
  const path = join(dir, 'random.bin');
  await writeSegment(terms, path);
  const reader = await SegmentReader.open(path);

  for (let q = 0; q < 50; q++) {
    const k = 1 + Math.floor(rand() * 4);
    const picked: string[] = [];
    for (let i = 0; i < k; i++) picked.push(`t${Math.floor(rand() * 20)}`);
    const uniq = [...new Set(picked)];
    const expected = rawIntersect(uniq.map((t) => [...new Set(terms.get(t)!)].sort((a, b) => a - b)));
    assert.deepEqual(reader.intersect(uniq), expected, `intersect(${uniq})`);
  }
});

test('截断：文件的每一个字节前缀都无法通过校验', () => {
  const { buf } = buildSegmentBuffer([
    ['alpha', [1, 2, 3, 4, 5]],
    ['beta', [10, 20, 30]],
    ['gamma', [7]],
  ]);
  assert.ok(validateSegmentBuffer(buf).ok, 'full file must validate');
  for (let n = 0; n < buf.length; n++) {
    const truncated = buf.subarray(0, n);
    const report = validateSegmentBuffer(Buffer.from(truncated));
    assert.ok(!report.ok, `truncation to ${n} bytes must be rejected`);
    assert.ok(report.issues.length > 0);
    for (const issue of report.issues) {
      assert.ok(issue.byteStart >= 0 && issue.byteEnd <= n, `issue range inside truncated file: ${JSON.stringify(issue)}`);
    }
  }
});

test('交换两个内容各自合法的块仍被检出（词项绑定）', () => {
  // 两个词项等长、块等长，交换后每块自身仍是合法编码。
  const aa: number[] = [];
  const bb: number[] = [];
  for (let i = 0; i < 128; i++) {
    aa.push(1000 + i);
    bb.push(2000 + i);
  }
  const { buf } = buildSegmentBuffer([
    ['aa', aa],
    ['bb', bb],
  ]);
  assert.ok(validateSegmentBuffer(buf).ok);

  const header = decodeHeader(buf);
  const entries = parseDictionary(buf, header.dictOffset, header.dictLength, header.termCount);
  const [metaA] = entries.find((e) => e.term === 'aa')!.blocks;
  const [metaB] = entries.find((e) => e.term === 'bb')!.blocks;
  assert.equal(metaA.length, metaB.length, 'test requires equal-length blocks');

  const swapped = Buffer.from(buf);
  const blockA = buf.subarray(metaA.offset, metaA.offset + metaA.length);
  const blockB = buf.subarray(metaB.offset, metaB.offset + metaB.length);
  blockA.copy(swapped, metaB.offset);
  blockB.copy(swapped, metaA.offset);

  const report = validateSegmentBuffer(swapped);
  assert.ok(!report.ok, 'swapped blocks must be rejected');
  const found = codes(report.issues);
  assert.ok(found.includes('TERM_MISMATCH'), `expected TERM_MISMATCH, got ${found}`);
  assert.ok(found.includes('CRC_MISMATCH'), `expected postings CRC_MISMATCH, got ${found}`);
  const termIssue = report.issues.find((i) => i.code === 'TERM_MISMATCH')!;
  assert.equal(termIssue.term, 'aa');
  assert.equal(termIssue.byteStart, metaA.offset);
});

test('非递增ID被检出', () => {
  const { buf } = buildSegmentBuffer([['t', [10, 20, 30]]]);
  const header = decodeHeader(buf);
  const [meta] = parseDictionary(buf, header.dictOffset, header.dictLength, header.termCount)[0].blocks;
  // 块头 = 2 + 1(词项't') + 16；载荷第1个varint是10(1字节)，第2个是delta=10。
  const deltaOffset = meta.offset + 2 + 1 + 16 + 1;
  assert.equal(buf[deltaOffset], 10);
  const corrupted = Buffer.from(buf);
  corrupted[deltaOffset] = 0; // delta 0 -> 非递增
  const report = validateSegmentBuffer(corrupted);
  assert.ok(!report.ok);
  assert.ok(codes(report.issues).includes('NON_INCREASING_ID'), JSON.stringify(report.issues, null, 2));
  const issue = report.issues.find((i) => i.code === 'NON_INCREASING_ID')!;
  assert.equal(issue.term, 't');
  assert.equal(issue.byteStart, deltaOffset);
});

test('累积文档ID超出u32被检为整数溢出', () => {
  const { buf } = buildSegmentBuffer([['t', [MAX_DOC_ID - 10, MAX_DOC_ID]]]);
  const header = decodeHeader(buf);
  const [meta] = parseDictionary(buf, header.dictOffset, header.dictLength, header.termCount)[0].blocks;
  // 第一个varint占5字节，其后是delta=10的1字节。
  const deltaOffset = meta.offset + 2 + 1 + 16 + 5;
  assert.equal(buf[deltaOffset], 10);
  const corrupted = Buffer.from(buf);
  corrupted[deltaOffset] = 100; // MAX_DOC_ID-10+100 > 2^32-1
  const report = validateSegmentBuffer(corrupted);
  assert.ok(!report.ok);
  assert.ok(codes(report.issues).includes('INTEGER_OVERFLOW'), JSON.stringify(report.issues, null, 2));
});

test('词典中块偏移重叠被检出', () => {
  const ids: number[] = [];
  for (let i = 0; i < 300; i++) ids.push(i); // 3块: 128+128+44
  const { buf } = buildSegmentBuffer([['t', ids]]);
  assert.ok(validateSegmentBuffer(buf).ok);

  const header = decodeHeader(buf);
  // 词典布局: u16 termLen + term + u32 blockCount + 3 * 20字节块条目。
  const entriesStart = header.dictOffset + 2 + 1 + 4;
  const secondOffsetField = entriesStart + 20 + 4 + 4; // 第2个条目的offset字段
  const corrupted = Buffer.from(buf);
  corrupted.writeBigUInt64LE(corrupted.readBigUInt64LE(entriesStart + 8), secondOffsetField);
  const report = validateSegmentBuffer(corrupted);
  assert.ok(!report.ok);
  assert.ok(codes(report.issues).includes('BLOCK_OVERLAP'), JSON.stringify(report.issues, null, 2));
});

test('摘要损坏被检出：词典字节、摘要自身、文件长度', () => {
  const { buf } = buildSegmentBuffer([
    ['a', [1, 2]],
    ['b', [3, 4]],
  ]);
  const header = decodeHeader(buf);

  // 1) 修改词典区一个字节 -> dictCrc 不匹配
  const c1 = Buffer.from(buf);
  c1[header.dictOffset + 3] ^= 0xff;
  const r1 = validateSegmentBuffer(c1);
  assert.ok(!r1.ok);
  assert.ok(codes(r1.issues).includes('CRC_MISMATCH'));

  // 2) 修改摘要自身一个字节 -> summaryCrc 不匹配
  const c2 = Buffer.from(buf);
  c2[header.summaryOffset + 6] ^= 0xff;
  const r2 = validateSegmentBuffer(c2);
  assert.ok(!r2.ok);
  assert.ok(codes(r2.issues).some((c) => c === 'CRC_MISMATCH' || c === 'COUNT_MISMATCH'));

  // 3) 文件尾部追加垃圾 -> fileLength 不匹配
  const c3 = Buffer.concat([buf, Buffer.from([0xAA])]);
  const r3 = validateSegmentBuffer(c3);
  assert.ok(!r3.ok);
  assert.ok(codes(r3.issues).includes('FILE_LENGTH_MISMATCH'));
});

test('未通过校验的段拒绝提供查询', async () => {
  const { buf } = buildSegmentBuffer([['a', [1, 2, 3]]]);
  const corrupted = Buffer.from(buf);
  corrupted[HEADER_SIZE] ^= 0xff; // 破坏词典区
  const path = writeTmp(corrupted);
  await assert.rejects(() => SegmentReader.open(path), /invalid segment/);
});

test('seek 不会回退，且越过末尾后游标结束', async () => {
  const path = join(dir, 'seek.bin');
  await writeSegment([['s', [10, 20, 30, 40]]], path, { blockSize: 2 });
  const reader = await SegmentReader.open(path);
  const cursor = reader.cursor('s');
  cursor.seek(25);
  assert.equal(cursor.value, 30);
  cursor.seek(20); // 目标小于当前值 -> 不移动
  assert.equal(cursor.value, 30);
  cursor.seek(30); // 等于当前值 -> 不移动
  assert.equal(cursor.value, 30);
  cursor.seek(41); // 越过最大值 -> done
  assert.equal(cursor.done, true);
});
