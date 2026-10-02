/**
 * 验收测试：钻石依赖图
 *
 *     A
 *    / \
 *   B   C
 *    \ /
 *     D
 *
 * 更新 A 后：B、C 按需各更新一次，D 仅计算一次，
 * 且 D 读到的 B、C 均为最新版本，无中间瞬态脏值。
 */
import { test } from "node:test";
import assert from "node:assert/strict";
import { signal, computed, effect, batch } from "../src/reactive.ts";

test("钻石图：更新 A，D 只计算一次且读到一致的 B/C", () => {
  const a = signal(1);

  let bRuns = 0;
  let cRuns = 0;
  let dRuns = 0;
  /** D 每次计算时实际读到的 (b, c) 快照 */
  const dSnapshots: Array<[number, number]> = [];

  const b = computed(() => {
    bRuns++;
    return a.value + 1;
  });
  const c = computed(() => {
    cRuns++;
    return a.value * 2;
  });
  const d = computed(() => {
    dRuns++;
    const bv = b.value;
    const cv = c.value;
    dSnapshots.push([bv, cv]);
    return bv + cv;
  });

  // 惰性：未读取前不发生任何计算
  assert.equal(bRuns, 0);
  assert.equal(cRuns, 0);
  assert.equal(dRuns, 0);

  assert.equal(d.value, 4); // (1+1) + (1*2)
  assert.deepEqual(dSnapshots, [[2, 2]]);
  assert.equal(bRuns, 1);
  assert.equal(cRuns, 1);
  assert.equal(dRuns, 1);

  // 重复读取：缓存命中，不重算
  assert.equal(d.value, 4);
  assert.equal(dRuns, 1);

  // === 核心验收：更新 A ===
  a.value = 2;

  // 两阶段之阶段一已完成（染色），但 B/C/D 尚未重算（按需拉取）
  assert.equal(bRuns, 1);
  assert.equal(cRuns, 1);
  assert.equal(dRuns, 1);

  // 阶段二：读取 D，沿依赖边拉取最新值
  assert.equal(d.value, 7); // (2+1) + (2*2)

  // B、C 按需各更新一次
  assert.equal(bRuns, 2);
  assert.equal(cRuns, 2);
  // D 仅计算一次
  assert.equal(dRuns, 2);
  // D 读到的 B、C 均为最新版本：(3, 4)，不存在 (3, 2) / (2, 4) 之类的中间瞬态
  assert.deepEqual(dSnapshots, [
    [2, 2],
    [3, 4],
  ]);
});

test("钻石图 + Effect：每次更新只触发一次，快照始终一致", () => {
  const a = signal(1);
  const b = computed(() => a.value + 1);
  const c = computed(() => a.value * 2);

  let dRuns = 0;
  const d = computed(() => {
    dRuns++;
    return b.value + c.value;
  });

  const seen: number[] = [];
  const stop = effect(() => {
    seen.push(d.value);
  });

  assert.deepEqual(seen, [4]);
  assert.equal(dRuns, 1);

  a.value = 2;
  a.value = 3;
  a.value = 4;

  // 每次写入：Effect 运行一次、D 计算一次，值始终为一致快照
  assert.deepEqual(seen, [4, 7, 10, 13]);
  assert.equal(dRuns, 4);

  stop();
  a.value = 5;
  assert.deepEqual(seen, [4, 7, 10, 13]); // dispose 后不再触发
});

test("钻石图 + batch：多次写入合并为一个更新周期", () => {
  const a = signal(1);

  let dRuns = 0;
  const dSnapshots: Array<[number, number]> = [];
  const b = computed(() => a.value + 1);
  const c = computed(() => a.value * 2);
  const d = computed(() => {
    dRuns++;
    const snapshot: [number, number] = [b.value, c.value];
    dSnapshots.push(snapshot);
    return snapshot[0] + snapshot[1];
  });

  const seen: number[] = [];
  effect(() => {
    seen.push(d.value);
  });

  batch(() => {
    a.value = 10;
    a.value = 20;
    a.value = 30;
  });

  // 整个 batch 只产生一次结算：D 只多算一次，Effect 只多跑一次
  assert.deepEqual(seen, [4, 91]); // (30+1) + (30*2) = 91
  assert.equal(dRuns, 2);
  assert.deepEqual(dSnapshots, [
    [2, 2],
    [31, 60],
  ]);
});

test("拓扑染色：节点 level 严格分层", () => {
  const a = signal(1);
  const b = computed(() => a.value + 1);
  const c = computed(() => a.value * 2);
  const d = computed(() => b.value + c.value);

  assert.equal(a.level, 0);
  // 读取以建立依赖与染色高度
  void d.value;
  assert.equal(b.level, 1);
  assert.equal(c.level, 1);
  assert.equal(d.level, 2);
  // 下游 level 恒大于所有上游
  assert.ok(d.level > b.level && d.level > c.level);
});

test("更宽钻石：A 驱动 B/C，D 汇合后再被 E 消费", () => {
  const a = signal(1);
  let eRuns = 0;
  const b = computed(() => a.value + 1);
  const c = computed(() => a.value * 10);
  const d = computed(() => b.value + c.value);
  const e = computed(() => {
    eRuns++;
    return d.value * 2;
  });

  assert.equal(e.value, 24); // ((1+1)+(1*10))*2
  assert.equal(eRuns, 1);

  a.value = 5;
  assert.equal(e.value, 112); // ((5+1)+(5*10))*2
  assert.equal(eRuns, 2); // 只多算一次
});
