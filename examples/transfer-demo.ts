/**
 * 运行样例：分库1 与 分库2 之间的跨分片转账
 *   node examples/transfer-demo.ts
 */
import { GlobalReadViewAligner } from '../src/aligner.ts';
import { Shard } from '../src/shard.ts';

const shard1 = new Shard('shard-1'); // 账户 A 所在分库
const shard2 = new Shard('shard-2'); // 账户 B 所在分库
const aligner = new GlobalReadViewAligner(['shard-1', 'shard-2'], { A: 100, B: 50 });

function tryQuery(label: string) {
  const view = aligner.getReadView();
  if (view.status === 'blocked') {
    console.log(`[${label}] ❌ 阻塞：${view.reason} | pending=[${view.pendingTxns}]`);
  } else {
    const total = Object.values(view.balances).reduce((a, b) => a + b, 0);
    console.log(`[${label}] ✅ 快照就绪 T_Safe=${view.safeTs} balances=${JSON.stringify(view.balances)} 总资金=${total}`);
  }
}

console.log('--- 跨分片转账：A(分库1) 向 B(分库2) 转账 30 ---\n');

// 1. 分库1 提交转出事件（A -30），分库2 的转入尚未就绪
aligner.ingest(shard1.debitEvent('txn-1', 'A', 30, 2, ['shard-1', 'shard-2']));
aligner.heartbeat({ shardId: 'shard-1', ts: 10 });
aligner.heartbeat({ shardId: 'shard-2', ts: 10 });
tryQuery('转出已提交/转入未就绪');

// 2. 分库2 的转入事件（B +30）延迟到达，事务因果闭合
aligner.ingest(shard2.creditEvent('txn-1', 'B', 30, 2, ['shard-1', 'shard-2']));
aligner.heartbeat({ shardId: 'shard-2', ts: 20 });
tryQuery('转入转出全部就绪');

console.log('\n--- 结论：闭合前绝不输出视图，闭合后资金守恒（150 -> 150） ---');
