import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { MatrixClock } from '../src/matrix-clock.ts';

function communicationRound(cluster: Cluster, tag: string): void {
  for (const node of cluster.nodes) node.localEvent(`${tag}: local op`);
  for (const node of cluster.nodes) {
    for (const peer of cluster.nodes) {
      if (peer.id !== node.id) node.send(peer.id, `${tag} payload`);
    }
  }
  cluster.runRound();
}

test('验收: 3 节点通信数轮后, 节点 A 判定 LSN<=10 的事件已被全网知悉并安全截断', () => {
  const cluster = new Cluster(3, ['A', 'B', 'C']);
  const [a] = cluster.nodes;

  // 每轮 A 追加 5 条日志; 推进通信直至矩阵时钟水位覆盖 LSN 1..10
  let round = 0;
  while (a.computeGcLsn() < 10 && round < 10) {
    round += 1;
    communicationRound(cluster, `r${round}`);
  }

  // 数轮通信内必然收敛
  assert.ok(round >= 2 && round <= 5, `expected convergence within a few rounds, got ${round}`);
  // 前两轮已产生 LSN 1..10, 收敛轮次后日志更多
  assert.ok(a.records().length >= 15);

  // 矩阵时钟判定: 安全水位 >= 10, 即 LSN<=10 的事件已被每个节点确认知悉
  const gcLsn = a.computeGcLsn();
  assert.ok(gcLsn >= 10, `GC safe LSN ${gcLsn} should cover 10`);

  // 水位语义验证: horizon[j] = min_i M[i][j], 即集群每个节点都知悉节点 j 推进到 horizon[j]
  const horizon = a.clock.horizon();
  for (let i = 0; i < 3; i++) {
    for (let j = 0; j < 3; j++) {
      assert.ok(a.clock.get(i, j) >= horizon[j]);
    }
  }

  // 安全性: LSN 1..10 中每条记录的时间戳都被水位逐分量支配
  const snapshot = a.records().filter((r) => r.lsn <= 10);
  assert.equal(snapshot.length, 10);
  for (const rec of snapshot) {
    assert.ok(
      MatrixClock.coveredBy(rec.timestamp, horizon),
      `LSN ${rec.lsn} ts=[${rec.timestamp}] must be covered by horizon [${horizon}]`,
    );
  }

  // 触发底层物理日志截断, 恰好截断到 LSN 10
  const truncated = a.truncateLog(10);
  assert.equal(truncated, 10);
  assert.equal(a.truncatedUpTo, 10);
  assert.equal(a.records()[0]!.lsn, 11, '物理日志应自 LSN 11 起');
  assert.ok(a.records().every((r) => r.lsn > 10));
});

test('安全性: 水位未覆盖时绝不截断', () => {
  const cluster = new Cluster(3, ['A', 'B', 'C']);
  const [a, b] = cluster.nodes;

  // A 产生事件但只通知了 B, C 完全不知情 -> A 的矩阵中 C 行全 0
  a.localEvent('op1');
  a.send(b.id, 'only-to-B');
  cluster.quiesce();

  assert.equal(a.computeGcLsn(), 0, 'C 尚未确认任何事件, 不允许回收');
  assert.equal(a.truncateLog(), 0, '物理截断不得发生');
  assert.equal(a.records().length, 2, '日志必须完整保留');
});

test('截断是连续前缀且幂等', () => {
  const cluster = new Cluster(3, ['A', 'B', 'C']);
  const [a] = cluster.nodes;
  for (let r = 1; r <= 4; r++) communicationRound(cluster, `r${r}`);

  const first = a.truncateLog();
  assert.ok(first > 0);
  const remaining = a.records().map((r) => r.lsn);
  // 剩余 LSN 连续且从 first+1 开始
  assert.deepEqual(remaining, remaining.map((_, i) => first + 1 + i));
  // 无新通信时再次截断不会推进
  assert.equal(a.truncateLog(), first);
});
