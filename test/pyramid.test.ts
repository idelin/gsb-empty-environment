import { test } from "node:test";
import assert from "node:assert/strict";
import { Pyramid, makeKernel, mirror, type Block } from "../src/pyramid.ts";

/* ---------- 独立参考实现（不分块、无调度，直接全图计算） ---------- */

function refMirror(index: number, size: number): number {
  if (size <= 1) return 0;
  const period = 2 * size - 2;
  const m = ((index % period) + period) % period;
  return m < size ? m : period - m;
}

interface RefLevel {
  data: Float32Array;
  w: number;
  h: number;
}

function refDownsample(prev: RefLevel, taps: readonly number[]): RefLevel {
  const w = Math.ceil(prev.w / 2);
  const h = Math.ceil(prev.h / 2);
  const r = taps.length / 2 - 1;
  const data = new Float32Array(w * h);
  for (let py = 0; py < h; py++) {
    for (let px = 0; px < w; px++) {
      let sum = 0;
      for (let ky = 0; ky < taps.length; ky++) {
        for (let kx = 0; kx < taps.length; kx++) {
          const cx = refMirror(2 * px + kx - r, prev.w);
          const cy = refMirror(2 * py + ky - r, prev.h);
          sum += taps[kx] * taps[ky] * prev.data[cy * prev.w + cx];
        }
      }
      data[py * w + px] = sum;
    }
  }
  return { data, w, h };
}

function refPyramid(base: RefLevel, taps: readonly number[]): RefLevel[] {
  const levels = [base];
  while (levels[levels.length - 1].w > 1 || levels[levels.length - 1].h > 1) {
    levels.push(refDownsample(levels[levels.length - 1], taps));
  }
  return levels;
}

/** 与参考实现逐层逐像素比对。 */
function assertMatchesRef(p: Pyramid, base: RefLevel, taps: readonly number[]): void {
  assert.equal(p.isClean, true, "金字塔应已收敛（无脏块）");
  const ref = refPyramid(base, taps);
  assert.equal(p.levels.length, ref.length, "层数一致");
  for (let L = 0; L < ref.length; L++) {
    const got = p.toArray(L);
    assert.equal(p.levels[L].width, ref[L].w, `层 ${L} 宽度`);
    assert.equal(p.levels[L].height, ref[L].h, `层 ${L} 高度`);
    for (let i = 0; i < got.length; i++) {
      assert.ok(
        Math.abs(got[i] - ref[L].data[i]) < 1e-6,
        `层 ${L} 像素 ${i}: 实际 ${got[i]} 期望 ${ref[L].data[i]}`,
      );
    }
  }
}

/** 对参考第 0 层施加同样的编辑。 */
function refEdit(base: RefLevel, rect: { x: number; y: number; width: number; height: number }, v: number): void {
  for (let y = rect.y; y < rect.y + rect.height; y++) {
    for (let x = rect.x; x < rect.x + rect.width; x++) {
      base.data[y * base.w + x] = v;
    }
  }
}

/** 确定性、非对称的像素图案，可暴露镜像/转置类错误。 */
function pattern(x: number, y: number): number {
  return ((x * 31 + y * 17 + x * x * 3 + y * y * 7 + x * y * 5) % 256) / 255;
}

function makeBase(w: number, h: number, fn: (x: number, y: number) => number): RefLevel {
  const data = new Float32Array(w * h);
  for (let y = 0; y < h; y++) for (let x = 0; x < w; x++) data[y * w + x] = fn(x, y);
  return { data, w, h };
}

const TAPS = makeKernel([1, 3, 3, 1]).taps;

/* ---------- 测试 ---------- */

test("初始构建：奇数尺寸多层金字塔与参考一致，重复 update 为空操作", () => {
  const p = new Pyramid({ width: 37, height: 29, blockSize: 8, initial: pattern });
  assert.equal(p.isClean, false, "构建后应存在待更新块");
  p.update();
  assertMatchesRef(p, makeBase(37, 29, pattern), TAPS);

  let recomputed = 0;
  p.onRecompute = () => recomputed++;
  assert.equal(p.update(), 0, "收敛后再次 update 不应重算任何块");
  assert.equal(recomputed, 0);
});

test("局部编辑：失效范围精确到块，且每块只重算一次", () => {
  const W = 40, H = 40, BS = 16, R = 1;
  const p = new Pyramid({ width: W, height: H, blockSize: BS, initial: pattern });
  p.update();

  const recomputed: string[] = [];
  p.onRecompute = (b: Block) => recomputed.push(`${b.level}:${b.bx},${b.by}`);

  // 编辑区域横跨块边界 (15..17 跨越 x=16)，且未对齐。
  p.edit({ x: 15, y: 17, width: 3, height: 2 }, 0.5);
  p.update();

  // 独立推导期望失效块集合：逐层 [ceil((x0-r-1)/2), floor((x1+r)/2)]。
  const expected = new Set<string>();
  let x0 = 15, x1 = 17, y0 = 17, y1 = 18;
  let w = W, h = H;
  for (let L = 1; ; L++) {
    w = Math.ceil(w / 2);
    h = Math.ceil(h / 2);
    const px0 = Math.max(0, Math.ceil((x0 - R - 1) / 2));
    const py0 = Math.max(0, Math.ceil((y0 - R - 1) / 2));
    const px1 = Math.min(w - 1, Math.floor((x1 + R) / 2));
    const py1 = Math.min(h - 1, Math.floor((y1 + R) / 2));
    for (let by = Math.floor(py0 / BS); by <= Math.floor(py1 / BS); by++) {
      for (let bx = Math.floor(px0 / BS); bx <= Math.floor(px1 / BS); bx++) {
        expected.add(`${L}:${bx},${by}`);
      }
    }
    if (w === 1 && h === 1) break;
    x0 = px0; x1 = px1; y0 = py0; y1 = py1;
  }

  assert.deepEqual([...recomputed].sort(), [...expected].sort(), "重算块集合应与精确失效范围一致");
  assert.equal(new Set(recomputed).size, recomputed.length, "每个脏块只应重算一次");
  assert.ok(expected.size > 0);

  // 数值正确性：与参考一致。
  const base = makeBase(W, H, pattern);
  refEdit(base, { x: 15, y: 17, width: 3, height: 2 }, 0.5);
  assertMatchesRef(p, base, TAPS);

  // 图外编辑为空操作。
  assert.equal(p.pendingTasks, 0);
  p.edit({ x: -100, y: -100, width: 5, height: 5 }, 1);
  p.edit({ x: 1000, y: 1000, width: 5, height: 5 }, 1);
  assert.equal(p.pendingTasks, 0, "完全在图外的编辑不应产生失效");
});

test("跨块编辑与跨块采样：编辑横跨 4 个子块，父块取样跨越块边界", () => {
  const W = 24, H = 24, BS = 8;
  const p = new Pyramid({ width: W, height: H, blockSize: BS, initial: pattern });
  p.update();

  const before = p.readPixel(1, 5, 5); // 父像素 (5,5) 足迹为子层 [9,12]x[9,12]，横跨 4 个子块
  const recomputed: string[] = [];
  p.onRecompute = (b: Block) => recomputed.push(`${b.level}:${b.bx},${b.by}`);

  // 只编辑子块 (1,1) 内部（像素 8..15 区域的一小部分）。
  p.edit({ x: 10, y: 10, width: 2, height: 2 }, 1.0);
  p.update();

  // 第 1 层失效像素 [ceil(8/2), floor(11/2)] = [4,5]，落在块 (0,0) 内：
  // 虽然编辑发生在子块 (1,1)，但父块 (0,0) 因跨块取样而失效。
  assert.ok(recomputed.includes("1:0,0"), "父块 (0,0) 应因跨块足迹被失效");
  assert.ok(!recomputed.includes("1:1,1"), "父块 (1,1) 不在失效范围内");
  assert.notEqual(p.readPixel(1, 5, 5), before, "父像素值应因邻块编辑而改变");

  const base = makeBase(W, H, pattern);
  refEdit(base, { x: 10, y: 10, width: 2, height: 2 }, 1.0);
  assertMatchesRef(p, base, TAPS);
});

test("边缘镜像延拓：角部编辑按 reflect101 采样", () => {
  // 全 0 图像，只把角像素 (0,0) 置 8。
  const p = new Pyramid({ width: 6, height: 6, blockSize: 4, initial: () => 0 });
  p.edit({ x: 0, y: 0, width: 1, height: 1 }, 8);
  p.update();

  // 手工推导（reflect101：mirror(-1)=1, mirror(0)=0）：
  // 层1 像素 (0,0) 仅 kx=ky=1（对应子层坐标 0）命中，值 = (3/8)^2 * 8 = 1.125。
  assert.ok(Math.abs(p.readPixel(1, 0, 0) - 1.125) < 1e-6, "角像素应按镜像延拓加权");
  // 若误用 clamp 延拓，结果会是 (4/8)^2 * 8 = 2，这里明确排除。
  assert.ok(Math.abs(p.readPixel(1, 0, 0) - 2) > 1e-3);

  // mirror 函数本身的性质。
  assert.equal(mirror(-1, 6), 1);
  assert.equal(mirror(6, 6), 4);
  assert.equal(mirror(0, 1), 0);

  const base = makeBase(6, 6, () => 0);
  refEdit(base, { x: 0, y: 0, width: 1, height: 1 }, 8);
  assertMatchesRef(p, base, TAPS);
});

test("连续两轮未完成更新：过期任务被跳过，最终结果与参考一致", () => {
  const W = 40, H = 40, BS = 8;
  const p = new Pyramid({ width: W, height: H, blockSize: BS, initial: pattern });
  p.update();
  const base = makeBase(W, H, pattern);

  // 第一轮：编辑 A，只完成部分更新。
  p.edit({ x: 4, y: 4, width: 8, height: 8 }, 0.9);
  refEdit(base, { x: 4, y: 4, width: 8, height: 8 }, 0.9);
  p.update(2);
  assert.equal(p.isClean, false, "第一轮更新未完成");

  // 第二轮：编辑 B（与 A 的向上传播在高层重叠），同样只完成部分更新。
  p.edit({ x: 12, y: 12, width: 10, height: 10 }, 0.1);
  refEdit(base, { x: 12, y: 12, width: 10, height: 10 }, 0.1);

  // 此时队列中应存在已过期任务（版本号落后于块当前版本）。
  const stale = p.peekTasks().filter((t) => t.version !== t.block.version);
  assert.ok(stale.length > 0, "第二轮编辑后队列中应存在过期任务");

  p.update(2);
  assert.equal(p.isClean, false, "第二轮更新仍未完成");

  // 跑完剩余任务：过期任务不得破坏收敛结果。
  p.update();
  assertMatchesRef(p, base, TAPS);
});

test("过期任务不得覆盖新版本父块（直接执行过期任务）", () => {
  const p = new Pyramid({ width: 32, height: 32, blockSize: 8, initial: pattern });
  p.update();
  const base = makeBase(32, 32, pattern);

  // 编辑 A：抓取一个 level>=1 的任务快照。
  p.edit({ x: 8, y: 8, width: 6, height: 6 }, 0.7);
  refEdit(base, { x: 8, y: 8, width: 6, height: 6 }, 0.7);
  const taskA = p.peekTasks().find((t) => t.block.level === 1)!;
  assert.ok(taskA, "应存在第 1 层任务");

  // 完成第一轮，再编辑 B（同一区域）：taskA 过期。
  p.update();
  p.edit({ x: 8, y: 8, width: 6, height: 6 }, 0.2);
  refEdit(base, { x: 8, y: 8, width: 6, height: 6 }, 0.2);
  assert.ok(taskA.block.version > taskA.version, "块版本应已前进");

  const before = p.toArray(1).slice();
  const committed = p.runTask(taskA);
  assert.equal(committed, false, "过期任务不得提交");
  assert.deepEqual(p.toArray(1), before, "过期任务不得修改任何数据");

  // 正常完成第二轮，结果与参考一致。
  p.update();
  assertMatchesRef(p, base, TAPS);
});

test("提交前发生新编辑：已算出的旧结果被丢弃，不得覆盖新版本父块", () => {
  const p = new Pyramid({ width: 32, height: 32, blockSize: 8, initial: pattern });
  p.update();
  const base = makeBase(32, 32, pattern);

  // 编辑 A 使目标块变脏，取出它的任务。
  p.edit({ x: 8, y: 8, width: 4, height: 4 }, 0.6);
  refEdit(base, { x: 8, y: 8, width: 4, height: 4 }, 0.6);
  const task = p.peekTasks().find((t) => t.block.level === 1)!;
  const target = task.block;
  const dataBefore = target.data.slice();

  // 模拟异步竞争：结果算完、提交之前，同一区域发生编辑 B。
  p.onBeforeCommit = () => {
    p.edit({ x: 9, y: 9, width: 2, height: 2 }, 0.3);
  };
  const committed = p.runTask(task);
  p.onBeforeCommit = undefined;
  refEdit(base, { x: 9, y: 9, width: 2, height: 2 }, 0.3);

  assert.equal(committed, false, "提交时版本已过期，旧结果必须被丢弃");
  assert.deepEqual(target.data, dataBefore, "旧结果不得写入新版本父块");
  assert.notEqual(target.cleanVersion, target.version, "目标块应仍处于脏状态，等待重算");

  // 后续更新（含编辑 B 入队的新任务）收敛到正确结果。
  p.update();
  assertMatchesRef(p, base, TAPS);
});

test("随机压力：多轮随机编辑与随机预算的未完成更新，最终收敛到参考", () => {
  const configs = [
    { seed: 0x2f6e2b1, bs: 8, kernel: [1, 3, 3, 1] },
    { seed: 0x1b873593, bs: 4, kernel: [1, 3, 3, 1] },
    { seed: 0x5deece5d, bs: 16, kernel: [1, 5, 10, 10, 5, 1] }, // 半径 2 的核
    { seed: 0x0badf00d, bs: 8, kernel: [1, 5, 10, 10, 5, 1] },
  ];
  for (const cfg of configs) {
    // 确定性伪随机序列。
    let seed = cfg.seed;
    const rand = () => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff;
      return seed / 0x7fffffff;
    };
    const taps = makeKernel(cfg.kernel).taps;

    const W = 53, H = 41, BS = cfg.bs;
    const p = new Pyramid({ width: W, height: H, blockSize: BS, kernel: cfg.kernel, initial: pattern });
    const base = makeBase(W, H, pattern);
    p.update();

    for (let round = 0; round < 30; round++) {
      // 每轮 1~3 次随机编辑。
      const edits = 1 + Math.floor(rand() * 3);
      for (let e = 0; e < edits; e++) {
        const rect = {
          x: Math.floor(rand() * (W - 4)) - 2, // 允许部分越界
          y: Math.floor(rand() * (H - 4)) - 2,
          width: 1 + Math.floor(rand() * 9),
          height: 1 + Math.floor(rand() * 9),
        };
        const v = rand();
        p.edit(rect, v);
        // 参考侧同步编辑（手动裁剪到图内）。
        const x0 = Math.max(0, rect.x), y0 = Math.max(0, rect.y);
        const x1 = Math.min(W - 1, rect.x + rect.width - 1);
        const y1 = Math.min(H - 1, rect.y + rect.height - 1);
        if (x0 <= x1 && y0 <= y1) {
          refEdit(base, { x: x0, y: y0, width: x1 - x0 + 1, height: y1 - y0 + 1 }, v);
        }
      }
      // 随机预算的未完成更新（可能为 0，即整轮不更新）。
      p.update(Math.floor(rand() * 5));
    }

    p.update();
    assertMatchesRef(p, base, taps);
  }
});
