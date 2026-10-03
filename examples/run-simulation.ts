import { runExperiment, DEFAULT_CONFIG } from '../src/experiment.ts';

const startedAt = Date.now();
const r = runExperiment();
const elapsed = ((Date.now() - startedAt) / 1000).toFixed(1);

const line = '-'.repeat(64);
console.log('Sub-cluster Gossip 仿真样例 (虚拟时钟, 确定性种子)');
console.log(line);
console.log(`节点规模            : ${r.nodeCount} 节点 / ${r.cellCount} 个 Cluster Cell`);
console.log(`Cell 内心跳         : 每 ${DEFAULT_CONFIG.cellSize} 节点一环, 2s 间隔, 度 2`);
console.log(`跨 Cell 汇聚        : Leader 每 2s 向 3 个对等 Cell 汇聚广播`);
console.log(`扁平基线            : fanout ${DEFAULT_CONFIG.flatFanout}, 周期 ${DEFAULT_CONFIG.flatPeriodMs}ms`);
console.log(line);
console.log(`扁平基线 单节点包率 : ${r.flatPerNodePerSec.toFixed(2)} 包/s (入+出)`);
console.log(`分片协议 单节点包率 : ${r.subPerNodePerSec.toFixed(2)} 包/s (入+出)`);
console.log(`网络包降噪          : ${r.reductionPct.toFixed(2)}%  (目标 >= 95%)`);
console.log(line);
console.log(`崩溃节点            : #${r.crashNodeId} (t=${DEFAULT_CONFIG.crashAtMs / 1000}s)`);
console.log(`Cell 内感知         : ${r.cellPeersNotified}/${r.cellPeerTotal} 节点, 最大延迟 ${r.maxCellDetectionMs.toFixed(1)}ms (目标 < 300ms)`);
console.log(`跨 Cell 首知        : ${r.crossCellLearnedMs === null ? '未传播' : `+${r.crossCellLearnedMs.toFixed(1)}ms`}`);
console.log(line);
const pass = r.reductionPct >= 95 && r.maxCellDetectionMs < 300;
console.log(`验收结论            : ${pass ? 'PASS' : 'FAIL'}  (仿真耗时 ${elapsed}s)`);
process.exit(pass ? 0 : 1);
