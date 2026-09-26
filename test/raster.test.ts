import test from "node:test";
import assert from "node:assert/strict";
import {
  RasterImage,
  decodeRow,
  encodeRow,
  normalizeRuns,
  InvalidLabelError,
  DecodeLengthError,
  CorruptBlockError,
  BaselineMismatchError,
  BoundsError,
  type Label,
  type Patch,
} from "../src/raster.ts";
import { makeTestImage, rgbaToLabels, labelsToRgba } from "../src/rgba.ts";
import { RefGrid } from "../src/refgrid.ts";

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function fullImage(r: RasterImage): Label[] {
  return r.readRect(0, 0, r.width, r.height);
}

test("RGBA 小图逐像素参考比对：写入/平移/mask 混合操作", () => {
  const { img, palette } = makeTestImage();
  const labels = rgbaToLabels(img, palette);
  const r = new RasterImage(img.width, img.height, {
    labels: palette.map((_, i) => i),
    blockRows: 3,
  });
  const ref = new RefGrid(img.width, img.height, 0);
  r.writeRect(0, 0, img.width, img.height, labels);
  ref.writeRect(0, 0, img.width, img.height, labels);
  assert.deepEqual(fullImage(r), ref.all());

  r.writeRect(4, 2, 3, 5, 4);
  ref.writeRect(4, 2, 3, 5, 4);
  r.translateRegion(2, 1, 5, 4, 3, 3);
  ref.translateRegion(2, 1, 5, 4, 3, 3);
  const mask = [1, 0, 1, 0, 1, 0, 0, 1, 1, 0, 1, 0];
  r.applyMask(8, 6, 4, 3, mask, 1);
  ref.applyMask(8, 6, 4, 3, mask, 1);
  assert.deepEqual(fullImage(r), ref.all());

  const roundTrip = labelsToRgba(img.width, img.height, fullImage(r), palette);
  const refRgba = labelsToRgba(img.width, img.height, ref.all(), palette);
  assert.deepEqual([...roundTrip.data], [...refRgba.data]);
});

test("行内相邻同标签段合并，跨行同值不合并", () => {
  const r = new RasterImage(6, 4, { fill: 0 });
  r.writeRect(0, 0, 6, 4, 7);
  for (let y = 0; y < 4; y++) {
    assert.deepEqual(r.rowRuns(y), [{ label: 7, len: 6 }]);
  }
  r.writeRect(1, 0, 2, 1, 3);
  r.writeRect(3, 0, 2, 1, 3);
  assert.deepEqual(r.rowRuns(0), [
    { label: 7, len: 1 },
    { label: 3, len: 4 },
    { label: 7, len: 1 },
  ]);
  assert.deepEqual(normalizeRuns([{ label: 1, len: 2 }, { label: 1, len: 3 }, { label: 2, len: 1 }]), [
    { label: 1, len: 5 },
    { label: 2, len: 1 },
  ]);
});

test("块索引维护像素总数与标签计数", () => {
  const r = new RasterImage(10, 9, { blockRows: 4, fill: 0 });
  assert.equal(r.blockCount, 3);
  r.writeRect(0, 0, 10, 2, 5);
  const b0 = r.blockInfo(0);
  assert.equal(b0.pixelTotal, 40);
  assert.equal(b0.labelCounts.get(5), 20);
  assert.equal(b0.labelCounts.get(0), 20);
  assert.equal(r.blockInfo(2).pixelTotal, 10);
  r.validate();
});

test("小编辑只解压受影响行，不解压整图", () => {
  const r = new RasterImage(64, 64, { blockRows: 4, fill: 0 });
  r.resetStats();
  r.writeRect(10, 20, 3, 2, 9);
  assert.equal(r.stats.rowsDecoded, 2);
  r.resetStats();
  r.readRect(0, 0, 64, 1);
  assert.equal(r.stats.rowsDecoded, 1);
});

test("原地重叠平移按修改前快照读取", () => {
  const r = new RasterImage(8, 4, { fill: 0 });
  const ref = new RefGrid(8, 4, 0);
  const row0 = [1, 2, 3, 4, 5, 6, 7, 8];
  r.writeRect(0, 0, 8, 1, row0);
  ref.writeRect(0, 0, 8, 1, row0);
  r.translateRegion(0, 0, 6, 1, 2, 0);
  ref.translateRegion(0, 0, 6, 1, 2, 0);
  assert.deepEqual(fullImage(r), ref.all());
  assert.deepEqual(r.readRect(0, 0, 8, 1), [1, 2, 1, 2, 3, 4, 5, 6]);
  r.translateRegion(0, 0, 8, 3, 0, 1);
  ref.translateRegion(0, 0, 8, 3, 0, 1);
  assert.deepEqual(fullImage(r), ref.all());
});

test("非法标签写一半全回滚，旧快照不变", () => {
  const r = new RasterImage(8, 6, { labels: [0, 1, 2], blockRows: 2, fill: 0 });
  r.writeRect(0, 0, 8, 6, 1);
  const snap = r.snapshot();
  const before = fullImage(r);
  const bad = new Array(8 * 4).fill(2);
  bad[8 * 2 + 3] = 99;
  assert.throws(() => r.writeRect(0, 1, 8, 4, bad), InvalidLabelError);
  assert.deepEqual(fullImage(r), before);
  assert.deepEqual(r.compareWithSnapshot(snap), { shared: r.blockCount, total: r.blockCount });
  r.validate();
});

test("零宽区域是无操作", () => {
  const r = new RasterImage(8, 8, { fill: 0 });
  r.resetStats();
  const p = r.writeRect(3, 3, 0, 5, 7);
  assert.equal(p.changes.length, 0);
  assert.equal(r.stats.rowsDecoded, 0);
  assert.equal(r.version, 0);
  assert.deepEqual(fullImage(r), new Array(64).fill(0));
});

test("极长同值行保持单段且可正确读写", () => {
  const w = 1_000_000;
  const r = new RasterImage(w, 2, { fill: 4 });
  assert.deepEqual(r.rowRuns(0), [{ label: 4, len: w }]);
  r.resetStats();
  r.writeRect(500_000, 0, 3, 1, 9);
  assert.equal(r.stats.rowsDecoded, 1);
  assert.deepEqual(r.rowRuns(0), [
    { label: 4, len: 500_000 },
    { label: 9, len: 3 },
    { label: 4, len: w - 500_003 },
  ]);
  assert.deepEqual(r.readRect(499_999, 0, 5, 1), [4, 9, 9, 9, 4]);
  assert.equal(r.blockInfo(0).pixelTotal, w * 2);
  r.validate();
});

test("损坏块被校验与读取同时发现", () => {
  const r = new RasterImage(8, 4, { blockRows: 2, fill: 0 });
  r.writeRect(0, 1, 8, 1, 3);
  r.debugTamperRunLength(1, 0, 5);
  assert.throws(() => r.validate(), CorruptBlockError);
  assert.throws(() => r.readRect(0, 1, 8, 1), DecodeLengthError);
  assert.doesNotThrow(() => r.readRect(0, 2, 8, 1));
});

test("补丁可逆且带基线摘要，重复应用被拒绝", () => {
  const r = new RasterImage(10, 6, { blockRows: 2, fill: 0 });
  r.writeRect(0, 0, 10, 6, 1);
  const original = fullImage(r);
  const p: Patch = r.writeRect(2, 1, 4, 3, 8);
  assert.equal(p.baseline.width, 10);
  assert.equal(p.baseline.height, 6);
  assert.equal(p.baseline.affectedRows, 3);
  assert.notEqual(p.baseline.beforeHash, p.baseline.afterHash);
  const modified = fullImage(r);
  assert.throws(() => r.applyPatch(p), BaselineMismatchError);
  r.applyPatch(p, true);
  assert.deepEqual(fullImage(r), original);
  assert.throws(() => r.applyPatch(p, true), BaselineMismatchError);
  r.applyPatch(p);
  assert.deepEqual(fullImage(r), modified);
});

test("解码输入先校验长度和标签", () => {
  const ok = (l: Label) => l === 0 || l === 1;
  assert.throws(() => decodeRow([{ label: 0, len: 3 }], 4, ok), DecodeLengthError);
  assert.throws(() => decodeRow([{ label: 5, len: 4 }], 4, ok), InvalidLabelError);
  assert.throws(() => decodeRow([{ label: 0, len: 0 }], 4, ok), DecodeLengthError);
  assert.throws(() => decodeRow([{ label: 0, len: 5 }], 4, ok), DecodeLengthError);
  assert.deepEqual(decodeRow([{ label: 1, len: 2 }, { label: 0, len: 2 }], 4, ok), [1, 1, 0, 0]);
  assert.deepEqual(encodeRow([1, 1, 0, 0]).runs, [
    { label: 1, len: 2 },
    { label: 0, len: 2 },
  ]);
});

test("历史快照共享块统计与恢复", () => {
  const r = new RasterImage(16, 16, { blockRows: 4, fill: 0 });
  const s0 = r.snapshot();
  r.writeRect(0, 0, 4, 4, 2);
  const s1 = r.snapshot();
  r.writeRect(8, 12, 4, 4, 3);
  assert.deepEqual(r.compareWithSnapshot(s1), { shared: 3, total: 4 });
  assert.deepEqual(r.compareWithSnapshot(s0), { shared: 2, total: 4 });
  r.restoreSnapshot(s0);
  assert.deepEqual(fullImage(r), new Array(256).fill(0));
});

test("随机操作与参考模型逐像素一致", () => {
  const rand = mulberry32(12345);
  const r = new RasterImage(20, 14, { labels: [0, 1, 2, 3], blockRows: 3, fill: 0 });
  const ref = new RefGrid(20, 14, 0);
  const patches: Patch[] = [];
  for (let i = 0; i < 60; i++) {
    const w = 1 + Math.floor(rand() * 6);
    const h = 1 + Math.floor(rand() * 4);
    const x = Math.floor(rand() * (20 - w));
    const y = Math.floor(rand() * (14 - h));
    const kind = rand();
    if (kind < 0.4) {
      const label = Math.floor(rand() * 4);
      patches.push(r.writeRect(x, y, w, h, label));
      ref.writeRect(x, y, w, h, label);
    } else if (kind < 0.7) {
      const dx = Math.floor(rand() * 7) - 3;
      const dy = Math.floor(rand() * 5) - 2;
      patches.push(r.translateRegion(x, y, w, h, dx, dy));
      ref.translateRegion(x, y, w, h, dx, dy);
    } else {
      const mask = Array.from({ length: w * h }, () => (rand() < 0.5 ? 1 : 0));
      const label = Math.floor(rand() * 4);
      patches.push(r.applyMask(x, y, w, h, mask, label));
      ref.applyMask(x, y, w, h, mask, label);
    }
    assert.deepEqual(fullImage(r), ref.all(), `mismatch after op ${i}`);
  }
  r.validate();
  for (let i = patches.length - 1; i >= 0; i--) r.applyPatch(patches[i], true);
  assert.deepEqual(fullImage(r), new Array(20 * 14).fill(0));
});

test("越界矩形与非法构造被拒绝", () => {
  const r = new RasterImage(8, 8, { fill: 0 });
  assert.throws(() => r.writeRect(6, 0, 3, 1, 1), BoundsError);
  assert.throws(() => r.readRect(0, 7, 1, 2), BoundsError);
  assert.throws(() => new RasterImage(0, 4), BoundsError);
  assert.throws(() => new RasterImage(4, 4, { labels: [0], fill: 3 }), InvalidLabelError);
});
