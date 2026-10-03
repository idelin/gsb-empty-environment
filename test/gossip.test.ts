import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { GossipCluster } from '../src/gossip.ts';
import type { NodeMetadata } from '../src/types.ts';

const meta = (zone: string, weight: number, role = 'worker'): NodeMetadata => ({
  zone, weight, role, labels: { env: 'prod' }, attributes: {},
});

describe('Gossip 元数据同步', () => {
  it('动态更新权重 10 -> 80 后，经 gossip 全网收敛到最新值', () => {
    const cluster = new GossipCluster();
    cluster.addNode('node-a', '10.0.0.1:7946', meta('us-east', 10));
    cluster.addNode('node-b', '10.0.0.2:7946', meta('us-east', 50));
    cluster.addNode('node-c', '10.0.0.3:7946', meta('us-west', 90, 'leader'));

    // 更新前：其他节点看到 node-a 权重为 10
    assert.equal(cluster.get('node-b').get('node-a')!.metadata.weight, 10);

    // 动态更新：node-a 权重 10 -> 80
    cluster.get('node-a').updateLocalMetadata({ weight: 80 });

    // gossip 收敛后：所有节点视图一致且为最新值
    const rounds = cluster.converge();
    assert.ok(rounds >= 1 && rounds <= 50);
    for (const id of cluster.nodeIds()) {
      const view = cluster.get(id).get('node-a')!;
      assert.equal(view.metadata.weight, 80, `${id} 应看到 node-a 权重 80`);
      assert.equal(view.version, 2);
    }
  });

  it('并发冲突更新经 gossip 后全网收敛到同一结果', () => {
    const cluster = new GossipCluster();
    cluster.addNode('n1', '10.0.0.1:1', meta('us-east', 10));
    cluster.addNode('n2', '10.0.0.2:1', meta('us-east', 20));
    cluster.addNode('n3', '10.0.0.3:1', meta('us-east', 30));

    // 关于节点 n9 的两份同版本冲突更新，分别注入 n1 与 n2（模拟并发更新乱序到达）
    cluster.get('n1').applyRemote({
      nodeId: 'n9', address: '10.0.0.9:1',
      metadata: meta('us-east', 80), version: 5, heartbeat: 5, state: 'alive',
    });
    cluster.get('n2').applyRemote({
      nodeId: 'n9', address: '10.0.0.9:1',
      metadata: meta('us-east', 60), version: 5, heartbeat: 5, state: 'alive',
    });

    cluster.converge();
    const views = cluster.nodeIds().map((id) => cluster.get(id).get('n9')!.metadata);
    assert.deepEqual(views[0], views[1]);
    assert.deepEqual(views[1], views[2]);
  });

  it('dead 状态通过 gossip 传播', () => {
    const cluster = new GossipCluster();
    cluster.addNode('n1', '10.0.0.1:1', meta('us-east', 10));
    cluster.addNode('n2', '10.0.0.2:1', meta('us-east', 20));
    cluster.addNode('n3', '10.0.0.3:1', meta('us-west', 30));
    // n1 判定 n3 已死，该状态应随 gossip 传播给 n2
    cluster.get('n1').applyRemote({
      nodeId: 'n3', address: '10.0.0.3:1',
      metadata: meta('us-west', 30), version: 1, heartbeat: 1, state: 'dead',
    });
    cluster.converge();
    assert.equal(cluster.get('n1').get('n3')!.state, 'dead');
    assert.equal(cluster.get('n2').get('n3')!.state, 'dead');
  });
});
