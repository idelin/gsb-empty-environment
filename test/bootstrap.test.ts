import { test } from 'node:test';
import assert from 'node:assert/strict';
import { performance } from 'node:perf_hooks';
import { ClusterNode, type ClusterNodeOptions } from '../src/node.ts';
import { InMemoryNetwork } from '../src/transport.ts';

const GOSSIP_INTERVAL_MS = 20;

function makeNode(net: InMemoryNetwork, id: string, opts: Partial<ClusterNodeOptions> = {}) {
  return new ClusterNode({
    id,
    address: id,
    transport: net,
    gossipIntervalMs: GOSSIP_INTERVAL_MS,
    gossipSampleSize: 10,
    ...opts,
  });
}

async function stopAll(nodes: ClusterNode[]) {
  await Promise.all(nodes.map((n) => n.stop()));
}

/** 轮询等待条件成立，超时返回 false */
async function waitFor(cond: () => boolean, timeoutMs: number, stepMs = 10): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (cond()) return true;
    await new Promise((r) => setTimeout(r, stepMs));
  }
  return cond();
}

test('新集群组网：Seed 节点独立启动即成环', async () => {
  const net = new InMemoryNetwork();
  const seed = makeNode(net, 'seed-0', { isSeed: true });
  await seed.start();
  try {
    assert.equal(seed.bootstrapped, true);
    assert.equal(seed.gossipActive, true);
    assert.equal(seed.membership().length, 1);
    assert.equal(seed.membership()[0].id, 'seed-0');
  } finally {
    await seed.stop();
  }
});

test('验收：3 个全新节点仅配置单点 Seed，1 秒内拉取 50 节点元数据并融入洗牌环', async () => {
  const net = new InMemoryNetwork();
  const seed = makeNode(net, 'node-0', { isSeed: true });
  await seed.start();

  // 预建 50 节点集群（node-0 .. node-49，均经 Seed 冷启动入网）
  const cluster: ClusterNode[] = [seed];
  for (let i = 1; i < 50; i++) {
    cluster.push(makeNode(net, `node-${i}`, { seeds: ['node-0'] }));
  }
  await Promise.all(cluster.slice(1).map((n) => n.start()));
  assert.equal(seed.membership().length, 50, 'seed 应已掌握 50 个成员');

  // 3 个全新节点，仅知道单点 Seed 地址
  const fresh = ['fresh-1', 'fresh-2', 'fresh-3'].map((id) =>
    makeNode(net, id, { seeds: ['node-0'] }),
  );

  const t0 = performance.now();
  await Promise.all(fresh.map((n) => n.start()));
  const elapsedMs = performance.now() - t0;

  try {
    // 1 秒内完成 Bootstrap
    assert.ok(elapsedMs < 1000, `bootstrap 耗时 ${elapsedMs.toFixed(1)}ms，超过 1 秒上限`);

    for (const node of fresh) {
      // 拉取到全网 >=50 个节点元数据
      assert.ok(
        node.membership().length >= 50,
        `${node.meta.id} 仅知 ${node.membership().length} 个成员`,
      );
      // 立即融入 Gossip 洗牌环
      assert.equal(node.bootstrapped, true);
      assert.equal(node.gossipActive, true);
      assert.ok(node.ringSize >= 49, `${node.meta.id} 洗牌环规模仅 ${node.ringSize}`);
    }

    // Gossip 收敛：新节点元数据经洗牌环传播回既有成员
    const veterans = cluster.slice(1);
    const converged = await waitFor(
      () =>
        fresh.every((f) =>
          veterans.some((v) => v.membership().some((m) => m.id === f.meta.id)),
        ),
      1000,
    );
    assert.ok(converged, '1 秒内新节点应通过 Gossip 被既有成员发现');
  } finally {
    await stopAll([...cluster, ...fresh]);
  }
});

test('孤儿节点入网：Seed 未就绪时持续重试，Seed 上线后自动完成握手', async () => {
  const net = new InMemoryNetwork();
  const orphan = makeNode(net, 'orphan-1', {
    seeds: ['seed-late'],
    bootstrapTimeoutMs: 2000,
    bootstrapRetryMs: 30,
  });

  // Seed 尚未启动，握手在后台重试
  const started = orphan.start();
  await new Promise((r) => setTimeout(r, 150));
  assert.equal(orphan.bootstrapped, false);

  // Seed 迟到上线
  const lateSeed = makeNode(net, 'seed-late', { isSeed: true });
  await lateSeed.start();

  try {
    await started; // 孤儿节点应在超时前完成入网
    assert.equal(orphan.bootstrapped, true);
    assert.ok(orphan.membership().some((m) => m.id === 'seed-late'));
    assert.ok(lateSeed.membership().some((m) => m.id === 'orphan-1'));
  } finally {
    await stopAll([orphan, lateSeed]);
  }
});

test('孤儿节点入网：所有 Seed 均不可达且超时后报错', async () => {
  const net = new InMemoryNetwork();
  const orphan = makeNode(net, 'orphan-2', {
    seeds: ['dead-1', 'dead-2'],
    bootstrapTimeoutMs: 200,
    bootstrapRetryMs: 20,
  });
  await assert.rejects(orphan.start(), /bootstrap timed out/);
  assert.equal(orphan.bootstrapped, false);
  await orphan.stop();
});

test('Seed 故障转移：跳过不可达 Seed，使用列表中的下一个', async () => {
  const net = new InMemoryNetwork();
  const seedB = makeNode(net, 'seed-b', { isSeed: true });
  await seedB.start();

  const node = makeNode(net, 'node-x', { seeds: ['seed-dead', 'seed-b'] });
  try {
    await node.start();
    assert.equal(node.bootstrapped, true);
    assert.ok(node.membership().some((m) => m.id === 'seed-b'));
  } finally {
    await stopAll([node, seedB]);
  }
});

test('Gossip 洗牌环：节点下线后被对端移出成员表', async () => {
  const net = new InMemoryNetwork();
  const seed = makeNode(net, 'seed-0', { isSeed: true });
  await seed.start();
  const a = makeNode(net, 'a', { seeds: ['seed-0'] });
  const b = makeNode(net, 'b', { seeds: ['seed-0'] });
  await Promise.all([a.start(), b.start()]);

  // 等 b 感知 a
  assert.ok(await waitFor(() => b.membership().some((m) => m.id === 'a'), 1000));

  await a.stop(); // a 下线
  const evicted = await waitFor(() => !b.membership().some((m) => m.id === 'a'), 2000);
  try {
    assert.ok(evicted, 'a 下线后应被 b 从成员表中剔除');
  } finally {
    await stopAll([seed, b]);
  }
});
