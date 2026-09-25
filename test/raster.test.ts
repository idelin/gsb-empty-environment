import test from "node:test";
import assert from "node:assert/strict";
import { Raster } from "../src/raster.ts";
import { DecodeError, LabelError, decodeRuns } from "../src/rle.ts";
import {
  buildPalette,
  compareImages,
  imageToLabels,
  labelsToImage,
  makeTestImage,
} from "../src/rgba.ts";

function rasterFromLabels(labels: number[], w: number, h: number, labelCount: number): Raster {
  const raster = new Raster(w, h, { labelCount });
  raster.writeRect(0, 0, w, h, labels);
  raster.resetStats();
  return raster;
}

function assertRasterMatches(raster: Raster, expected: number[], w: number, h: number): void {
  const actual = raster.readRect(0, 0, w, h);
  for (let i = 0; i < expected.length; i++) {
    if (actual[i] !== expected[i]) {
      assert.fail(
        `pixel (${i % w}, ${Math.floor(i / w)}): expected ${expected[i]}, got ${actual[i]}`,
      );
    }
  }
}

function refWrite(ref: number[], rw: number, x: number, y: number, w: number, h: number, data: number[]): void {
  for (let dy = 0; dy < h; dy++) {
    for (let dx = 0; dx < w; dx++) {
      ref[(y + dy) * rw + (x + dx)] = data[dy * w + dx];
    }
  }
}

function refRead(ref: number[], rw: number, x: number, y: number, w: number, h: number): number[] {
  const out: number[] = [];
  for (let dy = 0; dy < h; dy++) {
    for (let dx = 0; dx < w; dx++) out.push(ref[(y + dy) * rw + (x + dx)]);
  }
  return out;
}

test("RGBA 小图逐像素往返一致", () => {
  const img = makeTestImage(16, 12);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster = rasterFromLabels(labels, 16, 12, palette.length);
  const back = raster.readRect(0, 0, 16, 12);
  const roundtrip = labelsToImage(back, 16, 12, palette);
  const diff = compareImages(img, roundtrip);
  assert.ok(diff.equal, `per-pixel mismatch: ${JSON.stringify(diff.firstDiffs)}`);
});

test("行内相邻同标签段合并", () => {
  const raster = new Raster(10, 1, { labelCount: 4 });
  raster.fillRect(2, 0, 3, 1, 1);
  assert.deepEqual(raster.getRowRuns(0), [
    { label: 0, length: 2 },
    { label: 1, length: 3 },
    { label: 0, length: 5 },
  ]);
  raster.fillRect(5, 0, 2, 1, 1);
  assert.deepEqual(raster.getRowRuns(0), [
    { label: 0, length: 2 },
    { label: 1, length: 5 },
    { label: 0, length: 3 },
  ]);
  raster.fillRect(0, 0, 10, 1, 1);
  assert.deepEqual(raster.getRowRuns(0), [{ label: 1, length: 10 }]);
  assert.ok(raster.validate().ok);
});

test("跨行同值不合并、不产生越界段", () => {
  const raster = new Raster(4, 3, { labelCount: 4 });
  raster.fillRect(0, 0, 4, 3, 1);
  for (let y = 0; y < 3; y++) {
    const runs = raster.getRowRuns(y);
    assert.equal(runs.length, 1);
    assert.equal(runs[0].length, 4, `row ${y} run must not exceed row width`);
  }
  const report = raster.validate();
  assert.ok(report.ok, report.errors.join("; "));
  assert.deepEqual(raster.blockTotals(), [12]);
});

test("重叠平移按修改前快照读", () => {
  const raster = new Raster(8, 1, { labelCount: 16 });
  raster.writeRect(0, 0, 8, 1, [0, 1, 2, 3, 4, 5, 6, 7]);
  raster.translate(0, 0, 7, 1, 1, 0);
  assert.deepEqual(raster.readRect(0, 0, 8, 1), [0, 0, 1, 2, 3, 4, 5, 6]);

  const img = makeTestImage(16, 12);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster2 = rasterFromLabels(labels, 16, 12, palette.length);
  const ref = labels.slice();
  const moves: Array<[number, number, number, number, number, number]> = [
    [2, 2, 8, 6, 3, 2],
    [4, 3, 8, 6, -2, 1],
    [0, 0, 16, 4, 0, 5],
  ];
  for (const [sx, sy, w, h, dx, dy] of moves) {
    raster2.translate(sx, sy, w, h, dx, dy);
    refWrite(ref, 16, sx + dx, sy + dy, w, h, refRead(ref, 16, sx, sy, w, h));
    assertRasterMatches(raster2, ref, 16, 12);
  }
});

test("布尔 mask 编辑", () => {
  const img = makeTestImage(12, 9);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster = rasterFromLabels(labels, 12, 9, palette.length);
  const ref = labels.slice();
  const mask: boolean[] = [];
  for (let i = 0; i < 6 * 5; i++) mask.push(i % 2 === 0);
  raster.applyMask(3, 2, 6, 5, mask, 1);
  const current = refRead(ref, 12, 3, 2, 6, 5);
  for (let i = 0; i < mask.length; i++) if (mask[i]) current[i] = 1;
  refWrite(ref, 12, 3, 2, 6, 5, current);
  assertRasterMatches(raster, ref, 12, 9);
  assert.ok(raster.validate().ok);
});

test("写到一半发现非法标签全回滚，旧快照不变", () => {
  const img = makeTestImage(8, 8);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster = rasterFromLabels(labels, 8, 8, palette.length);
  const snapId = raster.snapshot();
  const before = raster.readRect(0, 0, 8, 8);
  const patchesBefore = raster.patchHistory().length;

  const bad = new Array<number>(16).fill(1);
  bad[15] = 999;
  assert.throws(() => raster.writeRect(2, 2, 4, 4, bad), LabelError);

  assertRasterMatches(raster, before, 8, 8);
  assert.equal(raster.patchHistory().length, patchesBefore);
  assert.deepEqual(raster.snapshotSummary(snapId), raster.summary());
  raster.restore(snapId);
  assertRasterMatches(raster, before, 8, 8);
  assert.ok(raster.validate().ok);
});

test("零宽区域是无害空操作", () => {
  const raster = new Raster(6, 4, { labelCount: 4 });
  raster.fillRect(0, 0, 6, 4, 2);
  const before = raster.readRect(0, 0, 6, 4);
  const p1 = raster.writeRect(3, 1, 0, 2, []);
  const p2 = raster.fillRect(0, 0, 6, 0, 1);
  const p3 = raster.translate(1, 1, 0, 3, 2, 0);
  for (const patch of [p1, p2, p3]) assert.equal(patch.changes.length, 0);
  assertRasterMatches(raster, before, 6, 4);
  assert.ok(raster.validate().ok);
});

test("极长同值行保持单段且不解压", () => {
  const width = 1_000_000;
  const raster = new Raster(width, 2, { labelCount: 8 });
  raster.fillRect(0, 0, width, 1, 3);
  assert.deepEqual(raster.getRowRuns(0), [{ label: 3, length: width }]);
  assert.equal(raster.stats.rowsDecoded, 0, "write must not decompress rows");
  assert.deepEqual(raster.readRect(width - 10, 0, 10, 1), new Array(10).fill(3));
  assert.equal(raster.stats.rowsDecoded, 1, "read touches exactly one row");
  assert.deepEqual(raster.readRect(0, 1, 5, 1), new Array(5).fill(0));
  assert.ok(raster.validate().ok);
  assert.deepEqual(raster.blockTotals(), [width * 2]);
});

test("损坏块被 validate 检出", () => {
  const raster = new Raster(8, 40, { labelCount: 4, blockSize: 8 });
  raster.fillRect(0, 0, 8, 40, 1);
  assert.ok(raster.validate().ok);
  (raster as unknown as { rows: Array<Array<{ label: number; length: number }>> }).rows[17][0]
    .length += 3;
  const report = raster.validate();
  assert.equal(report.ok, false);
  assert.deepEqual(report.corruptedBlocks, [2]);
  assert.ok(report.errors.some((e) => e.includes("row 17")));
  assert.ok(report.errors.some((e) => e.includes("block 2")));
  assert.throws(() => raster.decodeAll(), DecodeError);
});

test("补丁可逆且带基线摘要", () => {
  const img = makeTestImage(16, 12);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster = rasterFromLabels(labels, 16, 12, palette.length);
  const initial = raster.readRect(0, 0, 16, 12);

  const summaryBefore = raster.summary();
  const p1 = raster.fillRect(1, 1, 5, 4, 2);
  assert.deepEqual(p1.baseline, summaryBefore);
  assert.deepEqual(p1.result, raster.summary());
  assert.notEqual(p1.baseline.checksum, p1.result.checksum);

  const p2 = raster.translate(0, 0, 8, 6, 4, 3);
  const p3 = raster.applyMask(2, 2, 4, 4, [1, 0, 1, 0, 0, 1, 0, 1, 1, 0, 1, 0, 0, 1, 0, 1], 3);

  raster.applyPatch(p3, { reverse: true });
  raster.applyPatch(p2, { reverse: true });
  raster.applyPatch(p1, { reverse: true });
  assertRasterMatches(raster, initial, 16, 12);
  assert.deepEqual(raster.summary(), summaryBefore);

  assert.throws(() => raster.applyPatch(p1, { reverse: true }), /mismatch/);
  raster.applyPatch(p1);
  raster.applyPatch(p2);
  raster.applyPatch(p3);
  assert.throws(() => raster.applyPatch(p1), /mismatch/);
  assert.ok(raster.validate().ok);
});

test("解码输入先校验长度和标签", () => {
  assert.throws(() => decodeRuns([{ label: 0, length: 3 }, { label: 1, length: 1 }], 5, 2), DecodeError);
  assert.throws(() => decodeRuns([{ label: 0, length: 6 }], 5, 2), DecodeError);
  assert.throws(() => decodeRuns([{ label: 7, length: 5 }], 5, 2), LabelError);
  assert.throws(() => decodeRuns([{ label: 0, length: 2 }, { label: 0, length: 3 }], 5, 2), DecodeError);
  assert.throws(() => decodeRuns([{ label: 0, length: 0 }, { label: 1, length: 5 }], 5, 2), DecodeError);
  assert.deepEqual(decodeRuns([{ label: 1, length: 4 }], 4, 2), [1, 1, 1, 1]);
});

test("小编辑不解压整图，快照间共享块", () => {
  const img = makeTestImage(32, 64);
  const palette = buildPalette(img);
  const labels = imageToLabels(img, palette);
  const raster = new Raster(32, 64, { labelCount: palette.length, blockSize: 8 });
  raster.writeRect(0, 0, 32, 64, labels);
  raster.resetStats();

  const snapA = raster.snapshot();
  raster.fillRect(10, 20, 2, 2, 1);
  assert.equal(raster.stats.rowsDecoded, 0, "small edit must not decode any row");
  const snapB = raster.snapshot();

  const share = raster.sharedBlocks(snapA, snapB);
  assert.equal(share.totalBlocks, 8);
  assert.equal(share.sharedBlocks, 7, "only the edited block differs");
  assert.equal(share.sharedRows, 62);

  raster.readRect(10, 20, 2, 2);
  assert.equal(raster.stats.rowsDecoded, 2, "read decodes only the touched rows");
});

test("块索引维护像素总数", () => {
  const raster = new Raster(16, 20, { labelCount: 8, blockSize: 8 });
  raster.fillRect(0, 0, 16, 20, 1);
  raster.fillRect(3, 3, 5, 5, 2);
  raster.translate(0, 0, 8, 8, 8, 8);
  raster.applyMask(0, 0, 4, 4, [1, 1, 0, 0, 1, 1, 0, 0, 0, 0, 1, 1, 0, 0, 1, 1], 4);
  assert.deepEqual(raster.blockTotals(), [128, 128, 64]);
  const report = raster.validate();
  assert.ok(report.ok, report.errors.join("; "));
});
