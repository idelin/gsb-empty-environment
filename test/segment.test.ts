import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { SegmentWriter } from "../src/writer.ts";
import { SegmentReader, SegmentValidationError } from "../src/reader.ts";
import { validateSegment } from "../src/validator.ts";
import { parseDictionary } from "../src/parse.ts";
import { BLOCK_HEADER_SIZE, MAX_DOC_ID } from "../src/format.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function buildBuffer(entries: [string, number[]][], blockSize = 4): Buffer {
  const w = new SegmentWriter();
  for (const [term, ids] of entries) w.addAll(term, ids);
  return w.toBuffer({ blockSize });
}

function rawQuery(entries: [string, number[]][], term: string): number[] {
  const found = entries.find(([t]) => t === term);
  if (!found) return [];
  return [...new Set(found[1])].sort((a, b) => a - b);
}

function rawAnd(entries: [string, number[]][], terms: string[]): number[] {
  const sets = terms.map((t) => new Set(rawQuery(entries, t)));
  if (sets.some((s) => s.size === 0) && terms.some((t) => rawQuery(entries, t).length === 0)) return [];
  const [first, ...rest] = sets;
  return [...first].filter((id) => rest.every((s) => s.has(id))).sort((a, b) => a - b);
}

test("round trip: duplicate IDs merged, sorted ascending", () => {
  const entries: [string, number[]][] = [
    ["apple", [5, 3, 9, 3, 5, 1]],
    ["banana", [2, 2, 2, 7]],
  ];
  const reader = SegmentReader.fromBuffer(buildBuffer(entries));
  assert.deepEqual(reader.query("apple"), [1, 3, 5, 9]);
  assert.deepEqual(reader.query("banana"), [2, 7]);
  assert.deepEqual(reader.query("missing"), []);
  assert.equal(reader.has("missing"), false);
});

test("empty term and empty posting list", () => {
  const entries: [string, number[]][] = [
    ["", [4, 8, 15]],
    ["nothing", []],
    ["something", [1]],
  ];
  const buf = buildBuffer(entries);
  const result = validateSegment(buf);
  assert.equal(result.ok, true, JSON.stringify(result.errors));
  const reader = SegmentReader.fromBuffer(buf);
  assert.deepEqual(reader.query(""), [4, 8, 15]);
  assert.deepEqual(reader.query("nothing"), []);
  assert.deepEqual(reader.andQuery(["", "something"]), []);
  assert.deepEqual(reader.andQuery(["", "missing-term"]), []);
});

test("max legal doc ID 0xFFFFFFFF round trips", () => {
  const entries: [string, number[]][] = [
    ["max", [0, 1, MAX_DOC_ID]],
    ["only-max", [MAX_DOC_ID]],
  ];
  const buf = buildBuffer(entries, 2);
  assert.equal(validateSegment(buf).ok, true);
  const reader = SegmentReader.fromBuffer(buf);
  assert.deepEqual(reader.query("max"), [0, 1, MAX_DOC_ID]);
  assert.deepEqual(reader.query("only-max"), [MAX_DOC_ID]);
  const c = reader.cursor("max")!;
  c.seek(MAX_DOC_ID);
  assert.equal(c.value, MAX_DOC_ID);
  assert.equal(c.advance(), undefined);
  const c2 = reader.cursor("max")!;
  c2.seek(2);
  assert.equal(c2.value, MAX_DOC_ID);
});

test("writer rejects out-of-range IDs", () => {
  const w = new SegmentWriter();
  assert.throws(() => w.add("t", -1), RangeError);
  assert.throws(() => w.add("t", MAX_DOC_ID + 1), RangeError);
  assert.throws(() => w.add("t", 1.5), RangeError);
});

test("cross-block AND intersection matches raw set operations", () => {
  const entries: [string, number[]][] = [
    ["even", Array.from({ length: 100 }, (_, i) => i * 2)],
    ["three", Array.from({ length: 67 }, (_, i) => i * 3)],
    ["five", Array.from({ length: 40 }, (_, i) => i * 5)],
  ];
  const reader = SegmentReader.fromBuffer(buildBuffer(entries, 4)); // 25/17/10 blocks
  assert.deepEqual(reader.andQuery(["even", "three"]), rawAnd(entries, ["even", "three"]));
  assert.deepEqual(reader.andQuery(["even", "three", "five"]), rawAnd(entries, ["even", "three", "five"]));
  assert.deepEqual(reader.andQuery(["even"]), rawQuery(entries, "even"));
});

test("seek skips blocks: decoded block count proves the jump table works", () => {
  const ids = Array.from({ length: 100 }, (_, i) => i); // 25 blocks of 4
  const reader = SegmentReader.fromBuffer(buildBuffer([["t", ids]], 4));

  const before = reader.stats.decodedBlocks;
  const c = reader.cursor("t")!;
  c.seek(96);
  assert.equal(c.value, 96);
  const decoded = reader.stats.decodedBlocks - before;
  assert.equal(decoded, 2, `seek(96) decoded ${decoded} blocks, expected only initial + target block`);

  // AND of two 100-id lists with disjoint high ranges: seeks skip blocks.
  const a = Array.from({ length: 100 }, (_, i) => i * 10); // 0..990
  const b = [...Array.from({ length: 50 }, (_, i) => i), ...Array.from({ length: 50 }, (_, i) => 500 + i * 10)];
  const r2 = SegmentReader.fromBuffer(buildBuffer([["a", a], ["b", b]], 4));
  const expected = a.filter((x) => new Set(b).has(x));
  const got = r2.andQuery(["a", "b"]);
  assert.deepEqual(got, expected);
  const totalBlocks = 25 + 25;
  assert.ok(
    r2.stats.decodedBlocks < totalBlocks,
    `decoded ${r2.stats.decodedBlocks} of ${totalBlocks} blocks; jump table should skip some`,
  );
});

test("seek lands on first ID >= target", () => {
  const ids = [1, 4, 9, 16, 25, 36, 49];
  const reader = SegmentReader.fromBuffer(buildBuffer([["sq", ids]], 3));
  const cases: [number, number | undefined][] = [
    [0, 1], [1, 1], [2, 4], [16, 16], [17, 25], [49, 49], [50, undefined],
  ];
  for (const [target, want] of cases) {
    const c = reader.cursor("sq")!;
    c.seek(target);
    assert.equal(c.value, want, `seek(${target})`);
  }
});

test("truncation at every byte position is rejected", () => {
  const buf = buildBuffer([["alpha", [1, 2, 3, 4, 5]], ["beta", [7, 8]], ["", [42]]], 2);
  assert.equal(validateSegment(buf).ok, true);
  for (let cut = 0; cut < buf.length; cut++) {
    const result = validateSegment(buf.subarray(0, cut));
    assert.equal(result.ok, false, `prefix of ${cut} bytes should be rejected`);
  }
});

test("swapping two content-valid blocks is detected with term and byte range", () => {
  // Two 4-ID blocks with equal encoded size so a raw swap keeps offsets valid.
  const buf = buildBuffer([["alpha", [10, 11, 12, 13, 20, 21, 22, 23]]], 4);
  assert.equal(validateSegment(buf).ok, true);
  const meta = parseDictionary(buf).find((m) => m.term === "alpha")!;
  assert.equal(meta.jump.length, 2);
  const [j0, j1] = meta.jump;
  const len0 = j1.offset - j0.offset;
  const len1 = meta.offset + meta.length - j1.offset;
  assert.equal(len0, len1, "test requires equal-size blocks");

  const swapped = Buffer.concat([
    buf.subarray(0, j0.offset),
    buf.subarray(j1.offset, j1.offset + len1),
    buf.subarray(j0.offset + len0, j1.offset),
    buf.subarray(j0.offset, j0.offset + len0),
    buf.subarray(j1.offset + len1),
  ]);
  const result = validateSegment(swapped);
  assert.equal(result.ok, false);
  const termErrors = result.errors.filter((e) => e.term === "alpha");
  assert.ok(termErrors.length > 0, "expected errors attributed to term 'alpha'");
  const inBlockRegion = termErrors.some(
    (e) => e.offset >= j0.offset && e.offset < j1.offset + len1,
  );
  assert.ok(inBlockRegion, "expected an error located inside the swapped block byte range");
  assert.ok(
    result.errors.some((e) => e.code === "digest-mismatch"),
    "digest should also flag the modified file",
  );
});

test("swapping blocks between two terms is detected via term binding", () => {
  const buf = buildBuffer([
    ["aaa", [10, 11, 12, 13]],
    ["bbb", [20, 21, 22, 23]],
  ], 4);
  const metas = parseDictionary(buf);
  const a = metas.find((m) => m.term === "aaa")!;
  const b = metas.find((m) => m.term === "bbb")!;
  const lenA = a.length;
  const lenB = b.length;
  assert.equal(lenA, lenB, "test requires equal-size term spans");
  const swapped = Buffer.concat([
    buf.subarray(0, a.offset),
    buf.subarray(b.offset, b.offset + lenB),
    buf.subarray(a.offset + lenA, b.offset),
    buf.subarray(a.offset, a.offset + lenA),
    buf.subarray(b.offset + lenB),
  ]);
  const result = validateSegment(swapped);
  assert.equal(result.ok, false);
  assert.ok(result.errors.some((e) => e.code === "term-binding"));
});

test("single-byte tampering in a posting payload is rejected", () => {
  const buf = buildBuffer([["x", [100, 200, 300, 400]]], 2);
  const meta = parseDictionary(buf)[0];
  const payloadByte = meta.jump[0].offset + BLOCK_HEADER_SIZE;
  const tampered = Buffer.from(buf);
  tampered[payloadByte] ^= 0x01;
  const result = validateSegment(tampered);
  assert.equal(result.ok, false);
});

test("reader refuses to serve queries from invalid segments", () => {
  const buf = buildBuffer([["t", [1, 2, 3]]], 2);
  const truncated = buf.subarray(0, buf.length - 10);
  assert.throws(() => SegmentReader.fromBuffer(truncated), SegmentValidationError);
  const dir = mkdtempSync(join(tmpdir(), "cidx-"));
  const path = join(dir, "seg.cidx");
  writeFileSync(path, truncated);
  assert.throws(() => SegmentReader.open(path), SegmentValidationError);
  writeFileSync(path, buf);
  const reader = SegmentReader.open(path); // valid file opens fine
  assert.deepEqual(reader.query("t"), [1, 2, 3]);
});

test("randomized cross-check against raw set operations", () => {
  const rand = mulberry32(20261004);
  for (let round = 0; round < 20; round++) {
    const universe = 1 + Math.floor(rand() * 300);
    const termCount = 1 + Math.floor(rand() * 8);
    const entries: [string, number[]][] = [];
    for (let t = 0; t < termCount; t++) {
      const ids: number[] = [];
      for (let id = 0; id < universe; id++) {
        if (rand() < 0.3) ids.push(id);
        if (rand() < 0.05) ids.push(id); // duplicates on purpose
      }
      entries.push([`term${t}`, ids]);
    }
    const blockSize = 1 + Math.floor(rand() * 7);
    const buf = buildBuffer(entries, blockSize);
    const result = validateSegment(buf);
    assert.equal(result.ok, true, `round ${round}: ${JSON.stringify(result.errors)}`);
    const reader = SegmentReader.fromBuffer(buf);
    for (const [term] of entries) {
      assert.deepEqual(reader.query(term), rawQuery(entries, term), `round ${round} term ${term}`);
    }
    for (let q = 0; q < 5; q++) {
      const picked = entries.filter(() => rand() < 0.6).map(([t]) => t);
      if (picked.length === 0) picked.push(entries[0][0]);
      assert.deepEqual(reader.andQuery(picked), rawAnd(entries, picked),
        `round ${round} AND ${picked}`);
    }
  }
});

test("empty index validates and answers empty", () => {
  const buf = new SegmentWriter().toBuffer();
  assert.equal(validateSegment(buf).ok, true);
  const reader = SegmentReader.fromBuffer(buf);
  assert.equal(reader.termCount, 0);
  assert.deepEqual(reader.query("anything"), []);
  assert.deepEqual(reader.andQuery(["anything"]), []);
});
