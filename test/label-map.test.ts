import { test } from "node:test";
import assert from "node:assert/strict";
import { LabelMap } from "../src/label-map.ts";
import { labelMapFromRGBA, makeRGBA, maskFromRGBA } from "../src/rgba.ts";

/** 把 LabelMap 的连通分量表示为规范形式：排序后的像素索引集合的多重集。 */
function canonicalComponents(map: LabelMap): string[] {
  return map
    .ids()
    .map((id) => map.pixelsOf(id).join(","))
    .sort();
}

/** 全量重标注的参考实现（测试用，与增量结果对照）。 */
function referenceComponents(mask: Uint8Array, width: number, height: number, connectivity: 4 | 8): string[] {
  const fresh = LabelMap.fromMask(mask, width, height, connectivity);
  return canonicalComponents(fresh);
}

test("对角像素：4 连通分开，8 连通相连（自建 RGBA 图）", () => {
  // 2x2，仅 (0,0) 与 (1,1) 为前景（对角）。
  const data = makeRGBA(2, 2, [
    [0, 0],
    [1, 1],
  ]);

  const map4 = labelMapFromRGBA(data, 2, 2, 4);
  assert.equal(map4.componentCount, 2);
  assert.notEqual(map4.labelAt(0, 0), map4.labelAt(1, 1));

  const map8 = labelMapFromRGBA(data, 2, 2, 8);
  assert.equal(map8.componentCount, 1);
  assert.equal(map8.labelAt(0, 0), map8.labelAt(1, 1));
});

test("删除桥像素使分量分裂", () => {
  // 3x1 横条，中间 (1,0) 是桥。
  const data = makeRGBA(3, 1, [
    [0, 0],
    [1, 0],
    [2, 0],
  ]);
  const map = labelMapFromRGBA(data, 3, 1, 4);
  assert.equal(map.componentCount, 1);
  const originalId = map.labelAt(0, 0);
  assert.equal(map.sizeOf(originalId), 3);

  map.setPixel(1, 0, false);

  assert.equal(map.componentCount, 2);
  assert.equal(map.labelAt(1, 0), 0); // 背景
  const leftId = map.labelAt(0, 0);
  const rightId = map.labelAt(2, 0);
  assert.notEqual(leftId, rightId);
  // 两块各 1 像素，并列时最小像素索引所在块保留原 ID。
  assert.equal(leftId, originalId);
  assert.equal(map.sizeOf(leftId), 1);
  assert.equal(map.sizeOf(rightId), 1);
});

test("拆分时最大块保留原 ID", () => {
  // 5x1 横条，删 (1,0)：左块 1 像素，右块 3 像素 -> 右块保留原 ID。
  const data = makeRGBA(5, 1, [
    [0, 0],
    [1, 0],
    [2, 0],
    [3, 0],
    [4, 0],
  ]);
  const map = labelMapFromRGBA(data, 5, 1, 4);
  const originalId = map.labelAt(0, 0);

  map.setPixel(1, 0, false);

  assert.equal(map.componentCount, 2);
  assert.equal(map.labelAt(4, 0), originalId); // 最大块保留原 ID
  assert.equal(map.sizeOf(originalId), 3);
  assert.notEqual(map.labelAt(0, 0), originalId);
  assert.equal(map.sizeOf(map.labelAt(0, 0)), 1);
});

test("添加桥像素使两个分量合并，保留最小 ID", () => {
  // 左右两个孤立像素，中间留空。
  const data = makeRGBA(3, 1, [
    [0, 0],
    [2, 0],
  ]);
  const map = labelMapFromRGBA(data, 3, 1, 4);
  assert.equal(map.componentCount, 2);
  const idA = map.labelAt(0, 0);
  const idB = map.labelAt(2, 0);
  const keep = Math.min(idA, idB);

  map.setPixel(1, 0, true);

  assert.equal(map.componentCount, 1);
  assert.equal(map.labelAt(0, 0), keep);
  assert.equal(map.labelAt(1, 0), keep);
  assert.equal(map.labelAt(2, 0), keep);
  assert.equal(map.sizeOf(keep), 3);
  assert.equal(map.sizeOf(Math.max(idA, idB)), 0); // 被合并的 ID 已消失
});

test("无关分量的标签在局部修改后保持稳定", () => {
  // 7x3：分量 A = 左竖条 (0,0..2)，分量 B = 右竖条 (6,0..2)，分量 C = 底部横条 (2..4, 2)。
  const data = makeRGBA(7, 3, [
    [0, 0],
    [0, 1],
    [0, 2],
    [6, 0],
    [6, 1],
    [6, 2],
    [2, 2],
    [3, 2],
    [4, 2],
  ]);
  const map = labelMapFromRGBA(data, 7, 3, 4);
  assert.equal(map.componentCount, 3);
  const idB = map.labelAt(6, 1);
  const idC = map.labelAt(3, 2);
  const labelsBefore = map.labelsArray();

  // 在分量 A 内部制造分裂：删掉 (0,1)，A 拆成两块。
  map.setPixel(0, 1, false);
  assert.equal(map.componentCount, 4);

  // B、C 的 ID 与每个像素的标签完全不变。
  assert.equal(map.labelAt(6, 0), idB);
  assert.equal(map.labelAt(6, 1), idB);
  assert.equal(map.labelAt(6, 2), idB);
  assert.equal(map.labelAt(2, 2), idC);
  assert.equal(map.labelAt(3, 2), idC);
  assert.equal(map.labelAt(4, 2), idC);
  for (const [x, y] of [
    [6, 0],
    [6, 1],
    [6, 2],
    [2, 2],
    [3, 2],
    [4, 2],
  ] as const) {
    assert.equal(map.labelAt(x, y), labelsBefore[y * 7 + x]);
  }

  // 再把 A 的碎片接回去（合并），B、C 依然不动。
  map.setPixel(0, 1, true);
  assert.equal(map.componentCount, 3);
  assert.equal(map.labelAt(6, 1), idB);
  assert.equal(map.labelAt(3, 2), idC);
});

test("8 连通下跨对角合并与拆分", () => {
  // 两个对角像素 + 一个桥像素 (1,0)：8 连通下三者本就连通。
  const data = makeRGBA(3, 2, [
    [0, 0],
    [1, 0],
    [2, 1],
  ]);
  const map = labelMapFromRGBA(data, 3, 2, 8);
  assert.equal(map.componentCount, 1);
  const originalId = map.labelAt(0, 0);

  // 删除桥 (1,0)：8 连通下 (0,0) 与 (2,1) 不再相邻 -> 分裂。
  map.setPixel(1, 0, false);
  assert.equal(map.componentCount, 2);
  assert.equal(map.labelAt(0, 0), originalId); // 并列大小，最小索引块保留原 ID
  assert.notEqual(map.labelAt(2, 1), originalId);
});

test("增量更新与全量重标注结果一致（随机操作）", () => {
  // 确定性伪随机数（mulberry32）。
  let state = 0x2f6e2b1;
  const rand = () => {
    state |= 0;
    state = (state + 0x6d2b79f5) | 0;
    let t = Math.imul(state ^ (state >>> 15), 1 | state);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  for (const connectivity of [4, 8] as const) {
    const width = 12;
    const height = 8;
    const mask = new Uint8Array(width * height);
    for (let i = 0; i < mask.length; i++) mask[i] = rand() < 0.4 ? 1 : 0;

    const map = LabelMap.fromMask(mask, width, height, connectivity);
    assert.deepEqual(canonicalComponents(map), referenceComponents(mask, width, height, connectivity));

    for (let step = 0; step < 300; step++) {
      const x = Math.floor(rand() * width);
      const y = Math.floor(rand() * height);
      const value = rand() < 0.5;
      map.setPixel(x, y, value);
      mask[y * width + x] = value ? 1 : 0;

      // 每步都与全量重标注对照分量结构（忽略具体 ID，只比像素分组）。
      assert.deepEqual(
        canonicalComponents(map),
        referenceComponents(mask, width, height, connectivity),
        `connectivity=${connectivity} step=${step} setPixel(${x},${y},${value})`,
      );
      // 尺寸表与像素数一致。
      let total = 0;
      for (const id of map.ids()) total += map.sizeOf(id);
      assert.equal(total, mask.reduce((a, b) => a + b, 0));
    }
  }
});

test("maskFromRGBA：默认谓词与自定义谓词", () => {
  // (0,0) 黑色不透明 -> 前景；(1,0) 白色不透明 -> 背景；(0,1) 黑色透明 -> 背景；
  // (1,1) 红色不透明：Rec.601 亮度约 76 < 128 -> 前景。
  const data = new Uint8ClampedArray([
    0, 0, 0, 255,
    255, 255, 255, 255,
    0, 0, 0, 0,
    255, 0, 0, 255,
  ]);
  const mask = maskFromRGBA(data, 2, 2);
  assert.deepEqual([...mask], [1, 0, 0, 1]);

  // 自定义谓词：alpha >= 128 即前景。
  const mask2 = maskFromRGBA(data, 2, 2, (_r, _g, _b, a) => a >= 128);
  assert.deepEqual([...mask2], [1, 1, 0, 1]);
});

test("重复设置相同值是安全的空操作", () => {
  const data = makeRGBA(2, 1, [[0, 0]]);
  const map = labelMapFromRGBA(data, 2, 1, 4);
  const id = map.labelAt(0, 0);
  map.setPixel(0, 0, true); // 已是前景
  map.setPixel(1, 0, false); // 已是背景
  assert.equal(map.componentCount, 1);
  assert.equal(map.labelAt(0, 0), id);
});
