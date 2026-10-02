import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Cluster } from '../src/cluster.ts';
import { mulberry32 } from '../src/random.ts';

/** 构造一个带确定性随机延迟、正在跑转账流量的 3 节点集群。 */
function makeCluster(seed: number): Cluster {
  const rand = mulberry32(seed);
  const cluster = new Cluster({
    balances: [1000, 2000, 3000],
    tickDelay: () => Math.floor(rand() * 3),
    netDelay: () => 1 + Math.floor(rand() * 5),
  });
  cluster.startTraffic();
  return cluster;
}

async function settle(cluster: Cluster): Promise<void> {
  cluster.stopTraffic();
  await cluster.network.drain();
}

test('并发转账中发起快照：本地状态 + 在途消息 = 总资产严格守恒', async (t) => {
  const cluster = makeCluster(42);
  t.after(() => settle(cluster));
  await new Promise((r) => setTimeout(r, 120));

  const report = await cluster.takeSnapshot('A', 'snap-1');

  assert.equal(report.localTotal + report.inTransitTotal, cluster.initialTotal);
  assert.equal(report.grandTotal, 6000);
  // 快照期间业务确实在并发进行（存在被捕获的在途消息）。
  assert.ok(cluster.transfersSent > 0, '快照前应有转账发生');

  await settle(cluster);
  assert.equal(cluster.liveTotal, cluster.initialTotal, '排空后全网实时资产也应守恒');
});

test('Marker 沿全网信道流动：所有进程均记录本地状态且所有入信道关闭', async (t) => {
  const cluster = makeCluster(7);
  t.after(() => settle(cluster));
  await new Promise((r) => setTimeout(r, 60));

  const report = await cluster.takeSnapshot('A', 'snap-2');

  // 3 个节点都留下了本地状态。
  assert.deepEqual(Object.keys(report.localStates).sort(), ['A', 'B', 'C']);
  // 全连接 3 节点 => 6 条有向信道全部有记录且已关闭。
  assert.equal(Object.keys(report.channelStates).length, 6);
  for (const p of cluster.processes) {
    assert.ok(p.isSnapshotComplete('snap-2'), `节点 ${p.id} 的快照应已完成`);
  }
});

test('一致割性质：在途消息恰好是"割前发送、割后接收"的消息', async (t) => {
  const cluster = makeCluster(99);
  t.after(() => settle(cluster));
  await new Promise((r) => setTimeout(r, 120));

  const report = await cluster.takeSnapshot('A', 'snap-3');

  for (const p of cluster.processes) {
    const snap = p.getSnapshot('snap-3')!;
    for (const ch of snap.channels.values()) {
      const senderSnap = cluster.processes
        .find((q) => q.id === ch.from)!
        .getSnapshot('snap-3')!;
      for (const m of ch.messages) {
        // 每条在途消息必须是发送方记录本地状态（割点）之前发出的：
        // 它在发送方扣款后、接收方入账前跨越了割，因此被信道状态捕获。
        assert.ok(
          m.senderClock <= senderSnap.cutClock,
          `${ch.from}->${p.id} 的消息 #${m.id} 应在发送方割点之前发送`,
        );
      }
    }
  }
  // 守恒是一致割的直接推论。
  assert.equal(report.grandTotal, cluster.initialTotal);
});

test('不同随机种子下快照均守恒（可复现）', async (t) => {
  const clusters: Cluster[] = [];
  t.after(() => Promise.all(clusters.map(settle)).then(() => {}));
  for (const seed of [1, 2, 3, 4, 5]) {
    const cluster = makeCluster(seed);
    clusters.push(cluster);
    await new Promise((r) => setTimeout(r, 80));
    const report = await cluster.takeSnapshot('A', `snap-seed-${seed}`);
    assert.equal(report.grandTotal, cluster.initialTotal, `seed=${seed} 应守恒`);
  }
});

test('无流量时快照：无在途消息，总资产等于本地状态之和', async () => {
  const cluster = new Cluster({ balances: [100, 200, 300] });
  const report = await cluster.takeSnapshot('A', 'snap-idle');
  assert.equal(report.inTransitTotal, 0);
  assert.equal(report.localTotal, 600);
  assert.equal(report.grandTotal, 600);
});
