/**
 * 运行样例：1000 节点下对比"分层 Sub-cluster Gossip"与"扁平全网广播"。
 *
 *   node src/sim/run.ts
 *
 * 输出：
 *  1. 两种拓扑的单节点每秒出入包数，以及降噪比；
 *  2. Cell 内注入节点崩溃后的局部感知延迟（目标 < 300ms）；
 *  3. 跨 Cell 广播的全网传播耗时。
 */

import { buildHierarchical, buildFlat } from '../scenario.ts';
import { perNodePacketRate, summarize } from '../metrics.ts';

const NODE_COUNT = 1000;
const SEED = 42;

function fmt(n: number, digits = 1): string {
  return n.toLocaleString('en-US', { maximumFractionDigits: digits });
}

// ---------------------------------------------------------------- 分层方案
console.log(`=== Sub-cluster Gossip 模拟（N=${NODE_COUNT}, seed=${SEED}）===\n`);

const hier = buildHierarchical(NODE_COUNT, SEED);
const cellSizes = hier.cells.map((c) => c.length);
console.log(
  `拓扑: ${hier.assignment.numCells} 个 Cluster Cell, ` +
    `大小 min=${Math.min(...cellSizes)} avg=${fmt(
      NODE_COUNT / hier.assignment.numCells,
    )} max=${Math.max(...cellSizes)}`,
);

// 预热 1.5s 进入稳态，随后清零计数器开始测量
hier.sim.runUntil(1500);
hier.net.resetCounters();

// t=2000：注入一条跨 Cell 广播；t=3000：崩溃一个节点
const broadcaster = hier.nodes[7];
hier.sim.schedule(500, () => broadcaster.broadcastData('config-v7', 1));
const victim = hier.nodes[633];
const victimCell = hier.cells[hier.assignment.cellOf(victim.id)];
hier.sim.schedule(1500, () => victim.crash());

hier.sim.runUntil(5000);
const hierRate = perNodePacketRate(hier.net, NODE_COUNT, 3500);

// 局部感知延迟：崩溃节点同 Cell 成员首次怀疑它的时刻 - 崩溃时刻
const crashAt = 3000;
const detectionLatencies = victimCell
  .filter((id) => id !== victim.id)
  .map((id) => hier.nodes[id].suspectAt[victim.id] - crashAt)
  .filter((d) => Number.isFinite(d) && d >= 0)
  .sort((a, b) => a - b);
const det = summarize(detectionLatencies);

// 跨 Cell 广播传播：统计学到 'config-v7' 的节点比例随时间的分布
const learnedAt = hier.nodes
  .map((n) => n.dataLearnedAt.get('config-v7'))
  .filter((t): t is number => t !== undefined)
  .sort((a, b) => a - b);
const reach99 = learnedAt[Math.floor(learnedAt.length * 0.99) - 1] - 2000;
const coverage = (learnedAt.length / NODE_COUNT) * 100;

console.log(
  `流量: 单节点 ${fmt(hierRate.outPerSec)} 包/秒 出 + ${fmt(
    hierRate.inPerSec,
  )} 包/秒 入 = ${fmt(hierRate.totalPerSec)} 包/秒`,
);
console.log(
  `局部故障感知: min=${det.min.toFixed(0)}ms p50=${det.p50.toFixed(0)}ms ` +
    `p95=${det.p95.toFixed(0)}ms max=${det.max.toFixed(0)}ms ` +
    `(${detectionLatencies.length}/${victimCell.length - 1} 个同 Cell 节点已感知)`,
);
console.log(
  `跨 Cell 广播: ${fmt(coverage)}% 节点收到, 99% 覆盖耗时 ${reach99}ms`,
);

// ---------------------------------------------------------------- 扁平基线
console.log(`\n=== 扁平全网广播基线（N=${NODE_COUNT}, 同心跳周期 25ms）===\n`);

const flat = buildFlat(NODE_COUNT, SEED);
flat.sim.runUntil(200); // 预热
flat.net.resetCounters();
flat.sim.runUntil(400); // 200ms 测量窗口（8 个广播轮次）
const flatRate = perNodePacketRate(flat.net, NODE_COUNT, 200);
console.log(
  `流量: 单节点 ${fmt(flatRate.outPerSec)} 包/秒 出 + ${fmt(
    flatRate.inPerSec,
  )} 包/秒 入 = ${fmt(flatRate.totalPerSec)} 包/秒`,
);

// ---------------------------------------------------------------- 对比结论
const reduction = 1 - hierRate.totalPerSec / flatRate.totalPerSec;
console.log(`\n=== 对比 ===\n`);
console.log(`降噪比: ${(reduction * 100).toFixed(2)}%（目标 ≥ 95%）`);
console.log(
  `局部感知延迟 max=${det.max.toFixed(0)}ms（目标 < 300ms）` +
    `${det.max < 300 ? ' ✓' : ' ✗'}`,
);
