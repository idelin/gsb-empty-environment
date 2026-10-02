/**
 * 运行样例：3 节点集群（A/B/C）持续并发转账，节点 A 发起
 * Chandy-Lamport 全局一致性快照，验证总资产严格守恒。
 *
 * 运行：node src/demo.ts   （需要 Node >= 22.6，推荐 >= 23）
 */
import { Cluster } from './cluster.ts';
import { mulberry32 } from './random.ts';

const rand = mulberry32(42);
const log = (line: string) => console.log(`  ${line}`);

const cluster = new Cluster({
  balances: [1000, 2000, 3000], // A=1000, B=2000, C=3000，总资产 6000
  tickDelay: () => Math.floor(rand() * 3), // 转账节奏 0~2ms
  netDelay: () => 1 + Math.floor(rand() * 5), // 网络延迟 1~5ms，制造在途消息
  log,
});

console.log('=== Chandy-Lamport 一致性快照演示 ===');
console.log(`初始余额: A=1000 B=2000 C=3000，总资产 = ${cluster.initialTotal}`);
console.log('启动并发转账流量（全网业务不暂停）...');
cluster.startTraffic();

// 让转账跑一会儿，确保快照时网络中确实存在在途消息。
await new Promise((r) => setTimeout(r, 120));
console.log(`已完成 ${cluster.transfersSent} 笔转账，当前在途消息 ${cluster.network.inFlightCount} 条`);

console.log('\n--- 节点 A 发起快照 snap-1，Marker 沿信道流动 ---');
const report = await cluster.takeSnapshot('A', 'snap-1');

console.log('\n--- 快照结果（一致割） ---');
for (const [id, balance] of Object.entries(report.localStates)) {
  console.log(`  节点 ${id} 本地状态: balance = ${balance}`);
}
console.log('  信道在途消息状态:');
for (const [channel, msgs] of Object.entries(report.channelStates)) {
  const sum = msgs.reduce((s, m) => s + m.amount, 0);
  console.log(`    ${channel}: ${msgs.length} 条在途转账，金额合计 ${sum}`);
}

console.log('\n--- 守恒校验 ---');
console.log(`  本地状态合计   = ${report.localTotal}`);
console.log(`  在途金额合计   = ${report.inTransitTotal}`);
console.log(`  快照总资产     = ${report.grandTotal}`);
console.log(`  初始总资产     = ${cluster.initialTotal}`);
console.log(`  守恒: ${report.grandTotal === cluster.initialTotal ? 'PASS ✔' : 'FAIL ✘'}`);

// 停掉流量并排干网络，再次核对全网实时总资产。
cluster.stopTraffic();
await cluster.network.drain();
console.log(`\n流量停止、网络排空后，全网实时总资产 = ${cluster.liveTotal}（应仍为 ${cluster.initialTotal}）`);

if (report.grandTotal !== cluster.initialTotal || cluster.liveTotal !== cluster.initialTotal) {
  console.error('\n资产不守恒，演示失败');
  process.exit(1);
}
console.log('\n演示成功：无需暂停业务即捕获一致割，总资产严格守恒。');
