/**
 * 验收测试（Node.js 内置测试运行器，TypeScript 直跑）：
 *
 *   node --test test/*.test.ts
 *
 * 覆盖规范验收边界：
 *  1. 1000 节点下，单节点每秒网络出入包数较扁平全网广播降低 ≥95%；
 *  2. Cell 内节点崩溃的局部感知延迟 < 300ms（且无误报）；
 *  3. 动态分片的均衡性与重分片低扰动；
 *  4. 跨 Cell 汇聚广播的全网可达性。
 */

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { buildHierarchical, buildFlat } from '../src/scenario.ts';
import { CellAssignment } from '../src/membership.ts';
import { perNodePacketRate, summarize } from '../src/metrics.ts';

const NODE_COUNT = 1000;

test('1000 节点：单节点每秒出入包数较扁平广播降低 >= 95%', () => {
  // 分层拓扑：预热 1s 进入稳态后，测量 1s 窗口
  const hier = buildHierarchical(NODE_COUNT, 42);
  hier.sim.runUntil(1000);
  hier.net.resetCounters();
  hier.sim.runUntil(2000);
  const hierRate = perNodePacketRate(hier.net, NODE_COUNT, 1000);

  // 扁平基线：相同心跳周期（25ms）的全网广播，测量 5 个轮次
  const flat = buildFlat(NODE_COUNT, 42);
  flat.sim.runUntil(100);
  flat.net.resetCounters();
  flat.sim.runUntil(225);
  const flatRate = perNodePacketRate(flat.net, NODE_COUNT, 125);

  const reduction = 1 - hierRate.totalPerSec / flatRate.totalPerSec;
  console.log(
    `    分层 ${hierRate.totalPerSec.toFixed(1)} 包/秒/节点 ` +
      `(出 ${hierRate.outPerSec.toFixed(1)} + 入 ${hierRate.inPerSec.toFixed(1)})\n` +
      `    扁平 ${flatRate.totalPerSec.toFixed(1)} 包/秒/节点 ` +
      `(出 ${flatRate.outPerSec.toFixed(1)} + 入 ${flatRate.inPerSec.toFixed(1)})\n` +
      `    降噪比 ${(reduction * 100).toFixed(2)}%`,
  );
  assert.ok(
    reduction >= 0.95,
    `降噪比 ${(reduction * 100).toFixed(2)}% 未达到 95%`,
  );
});

test('1000 节点：Cell 内节点崩溃的局部感知延迟 < 300ms', () => {
  const { sim, nodes, cells, assignment } = buildHierarchical(NODE_COUNT, 7);
  sim.runUntil(1500); // 预热，进入稳态

  const victim = nodes[633];
  const cellId = assignment.cellOf(victim.id);
  const mates = cells[cellId].filter((id) => id !== victim.id);
  const crashAt = sim.now;
  victim.crash();
  sim.runUntil(2500);

  const latencies = mates.map((id) => nodes[id].suspectAt[victim.id]);
  for (const t of latencies) {
    assert.ok(t >= crashAt, '存在漏报（未感知）或崩溃前误报');
  }
  const stats = summarize(latencies.map((t) => t - crashAt));
  console.log(
    `    局部感知延迟(${mates.length} 个同 Cell 节点): ` +
      `min=${stats.min}ms p50=${stats.p50}ms p95=${stats.p95}ms max=${stats.max}ms`,
  );
  assert.ok(stats.max < 300, `最大感知延迟 ${stats.max}ms 超过 300ms`);

  // 对存活节点零误报
  let falseSuspects = 0;
  for (const node of nodes) {
    for (const peer of cells[assignment.cellOf(node.id)]) {
      if (peer !== node.id && peer !== victim.id && node.suspectAt[peer] >= 0) {
        falseSuspects++;
      }
    }
  }
  assert.equal(falseSuspects, 0, `${falseSuspects} 起对存活节点的误报`);
});

test('动态分片：Cell 大小均衡，重分片低扰动', () => {
  const ids = Array.from({ length: NODE_COUNT }, (_, i) => i);
  const a32 = new CellAssignment(CellAssignment.recommendedCells(NODE_COUNT));
  const cells = a32.partition(ids);
  const avg = NODE_COUNT / a32.numCells;
  const sizes = cells.map((c) => c.length);
  const max = Math.max(...sizes);
  const min = Math.min(...sizes);
  console.log(
    `    ${a32.numCells} 个 Cell: min=${min} avg=${avg.toFixed(1)} max=${max}`,
  );
  assert.ok(max <= avg * 1.6, `最大 Cell ${max} 超过均值 1.6 倍`);
  assert.ok(min >= avg * 0.4, `最小 Cell ${min} 不足均值 0.4 倍`);

  // 规模变化触发 32 -> 33 个 Cell 的重分片：HRW 理论迁移率 ~1/33
  const a33 = new CellAssignment(33);
  let moved = 0;
  for (const id of ids) {
    if (a32.cellOf(id) !== a33.cellOf(id)) moved++;
  }
  console.log(`    重分片迁移率 ${((moved / NODE_COUNT) * 100).toFixed(1)}%`);
  assert.ok(moved / NODE_COUNT < 0.15, '重分片迁移率过高');
});

test('跨 Cell 汇聚广播：3s 内覆盖 >= 99% 节点', () => {
  const { sim, nodes } = buildHierarchical(NODE_COUNT, 99);
  sim.runUntil(1000);
  const injectAt = sim.now;
  nodes[0].broadcastData('config-v7', 1);
  sim.runUntil(5000);

  const times = nodes
    .filter((n) => n.dataVersion('config-v7') === 1)
    .map((n) => (n.dataLearnedAt.get('config-v7') as number) - injectAt)
    .sort((a, b) => a - b);
  const coverage = times.length / NODE_COUNT;
  const p99 = times[Math.floor(times.length * 0.99) - 1];
  console.log(
    `    广播覆盖 ${(coverage * 100).toFixed(1)}%，99% 覆盖耗时 ${p99}ms`,
  );
  assert.ok(coverage >= 0.99, `覆盖率 ${(coverage * 100).toFixed(1)}% 不足 99%`);
  assert.ok(p99 <= 3000, `99% 覆盖耗时 ${p99}ms 超过 3000ms`);
});
