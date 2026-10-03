import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { GossipCluster } from '../src/gossip.ts';
import { Router } from '../src/router.ts';
import { parseFilter, TopologyFilter } from '../src/filter.ts';
import type { NodeMetadata } from '../src/types.ts';

const meta = (zone: string, weight: number, role = 'worker', extra: Partial<NodeMetadata> = {}): NodeMetadata => ({
  zone, weight, role,
  labels: { env: 'prod' },
  attributes: {},
  ...extra,
});

function buildCluster(): GossipCluster {
  const cluster = new GossipCluster();
  cluster.addNode('node-a', '10.0.0.1:7946', meta('us-east', 10));
  cluster.addNode('node-b', '10.0.0.2:7946', meta('us-east', 50));
  cluster.addNode('node-c', '10.0.0.3:7946', meta('us-west', 90, 'leader'));
  return cluster;
}

describe('验收边界：动态权重更新 + gossip 同步 + 属性路由', () => {
  it("权重 10->80 更新并 gossip 后，route('zone=us-east') 返回权重最高的健康节点", () => {
    const cluster = buildCluster();
    const router = new Router(cluster.get('node-a'));

    // 更新前：us-east 中 node-b(50) 权重最高
    assert.equal(router.route('zone=us-east')!.nodeId, 'node-b');

    // 动态更新 node-a 权重 10 -> 80，并 gossip 同步
    cluster.get('node-a').updateLocalMetadata({ weight: 80 });
    cluster.converge();

    // 各节点路由视角一致：node-a(80) 成为 us-east 最优
    for (const id of cluster.nodeIds()) {
      const best = new Router(cluster.get(id)).route('zone=us-east');
      assert.equal(best!.nodeId, 'node-a', `${id} 视角应路由到 node-a`);
      assert.equal(best!.weight, 80);
    }

    // 完整子集按权重降序
    const subset = router.query('zone=us-east');
    assert.deepEqual(subset.map((r) => [r.nodeId, r.weight]), [['node-a', 80], ['node-b', 50]]);
  });

  it('不健康节点即使权重更高也被剔除', () => {
    let tick = 1_000;
    const cluster = new GossipCluster({ now: () => tick, suspectAfterMs: 100, deadAfterMs: 500 });
    cluster.addNode('heavy', '10.0.0.1:1', meta('us-east', 99));
    cluster.addNode('light', '10.0.0.2:1', meta('us-east', 10));
    tick += 1_000; // 远端心跳过期 -> dead
    const router = new Router(cluster.get('heavy'));
    const best = router.route('zone=us-east');
    assert.equal(best!.nodeId, 'heavy'); // light 已 dead，仅剩自身健康
  });
});

describe('TopologyFilter 属性筛选器', () => {
  it('支持 =、!=、>= 等运算符与多条件 AND', () => {
    const cluster = buildCluster();
    const router = new Router(cluster.get('node-a'));
    assert.equal(router.query('zone=us-east').length, 2);
    assert.equal(router.query('zone!=us-east').length, 1);
    assert.equal(router.query('weight>=50').length, 2);
    assert.equal(router.query('zone=us-east,weight>=50').length, 1);
    assert.equal(router.query('role=leader').length, 1);
    assert.equal(router.query('env=prod').length, 3); // labels 参与匹配
    assert.equal(router.query('zone=eu-west').length, 0);
  });

  it('route 在无匹配节点时返回 null', () => {
    const cluster = buildCluster();
    const router = new Router(cluster.get('node-a'));
    assert.equal(router.route('zone=eu-west'), null);
  });

  it('非法表达式抛出错误', () => {
    assert.throws(() => parseFilter('zone'), /missing operator/);
    assert.throws(() => parseFilter(''), /empty filter/);
    assert.throws(() => new TopologyFilter('=x'), /invalid filter term/);
  });
});
