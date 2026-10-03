import { Cluster } from '../src/paxos/cluster.ts';
import { formatValue } from '../src/paxos/types.ts';
import { formatBallot } from '../src/paxos/ballot.ts';

function showLogs(cluster: Cluster, label: string): void {
  console.log(`\n== ${label} ==`);
  for (const [id, node] of cluster.nodes) {
    const applied = node.applied.map(formatValue).join(', ');
    const role = node.isLeader ? `Leader${node.leaseValid ? '(lease)' : '(expired)'}` : 'Follower';
    console.log(
      `  ${id} [${role}] commitIndex=${node.commitIndex} promised=${formatBallot(node.promisedBallot)} applied=[${applied}]`,
    );
  }
  console.log(
    `  network: in-flight=${cluster.pendingMessages()} dropped=${cluster.network.droppedCount} ` +
      `prepare=${cluster.network.stat('prepare')} accept=${cluster.network.stat('accept')} commit=${cluster.network.stat('commit')}`,
  );
}

const cluster = new Cluster(5, 1000);
const n1 = cluster.node('n1');
const n3 = cluster.node('n3');

console.log('--- 1. n1 竞选并成为 Leader ---');
n1.campaign();
cluster.pump();
showLogs(cluster, 'n1 elected');

console.log('\n--- 2. 租约内连续提交（绕过 Phase 1） ---');
const preparesBefore = cluster.network.stat('prepare');
for (const v of ['set x=1', 'set y=2', 'set z=3']) n1.propose(v);
cluster.pump();
console.log(`  3 次提案新增 prepare 消息数: ${cluster.network.stat('prepare') - preparesBefore} (0 = 完全绕过 Phase 1)`);
showLogs(cluster, 'steady-state commits');

console.log('\n--- 3. 网络分区 {n1,n2} | {n3,n4,n5} ---');
cluster.partition(['n1', 'n2'], ['n3', 'n4', 'n5']);
n1.propose('dirty-A'); // 旧 Leader 在少数派的脏提案
n1.propose('dirty-B');
cluster.pump();
console.log('  旧 Leader 在少数派提案 dirty-A/dirty-B（无法提交）');
showLogs(cluster, 'partitioned: old leader dirty');

console.log('\n--- 4. 多数派选出新 Leader n3，Phase 1 修补后继续提交 ---');
cluster.advanceTime(1500); // 旧租约过期
n3.campaign();
cluster.pump();
n3.propose('set a=10');
n3.propose('set b=20');
cluster.pump();
showLogs(cluster, 'new leader commits in majority');

console.log('\n--- 5. 分区恢复，全网收敛，脏日志被安全覆写 ---');
cluster.heal();
n3.propose('set c=30');
cluster.pump();
showLogs(cluster, 'healed & converged');

const logs = cluster.committedLogs();
const converged = logs.every((log) => JSON.stringify(log) === JSON.stringify(logs[0]));
console.log(`\n全网日志收敛: ${converged ? 'OK' : 'FAILED'}`);
console.log(`在途消息(泄漏检查): ${cluster.pendingMessages()}`);
