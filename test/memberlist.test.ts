import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { MemberList } from '../src/memberlist.ts';
import { Router } from '../src/router.ts';
import type { NodeMetadata } from '../src/types.ts';

function meta(zone: string, weight: number, role: 'leader' | 'follower' | 'observer' = 'follower', labels: Record<string, string> = {}): NodeMetadata {
  return { zone, weight, role, labels };
}

function buildCluster(): MemberList[] {
  // 三个节点视角的成员列表，各自持有全量成员
  const lists = [new MemberList('n1'), new MemberList('n2'), new MemberList('n3')];
  for (const list of lists) {
    list.addNode('n1', '10.0.0.1:7000', meta('us-east', 10, 'leader', { tier: 'gold' }));
    list.addNode('n2', '10.0.0.2:7000', meta('us-east', 50, 'follower'));
    list.addNode('n3', '10.0.0.3:7000', meta('eu-west', 90, 'follower'));
  }
  return lists;
}

describe('MemberList 元数据与版本仲裁', () => {
  it('本地更新元数据：权重 10 -> 80，版本号单调递增', () => {
    const [ml] = buildCluster();
    const before = ml.get('n1')!.record;
    assert.equal(before.meta.weight, 10);

    const v = ml.updateMetadata('n1', { weight: 80 });
    const after = ml.get('n1')!.record;
    assert.equal(after.meta.weight, 80);
    assert.ok(after.version > before.version);
    assert.equal(v, after.version);
  });

  it('并发更新仲裁：高版本获胜，同版本按 updatedBy 字典序收敛', () => {
    const [a, b] = buildCluster();

    // 两个节点并发修改 n1 的权重
    a.updateMetadata('n1', { weight: 80 });
    b.updateMetadata('n1', { weight: 30 });

    // 双向 gossip 后必须收敛到同一值
    a.gossipWith(b);
    b.gossipWith(a);
    const wa = a.get('n1')!.record.meta.weight;
    const wb = b.get('n1')!.record.meta.weight;
    assert.equal(wa, wb);

    // 过期记录不得覆盖新记录
    const stale = { meta: meta('us-east', 1), version: 1, updatedBy: 'n9', updatedAt: 0 };
    assert.equal(a.applyRemote('n1', '10.0.0.1:7000', stale), false);
    assert.equal(a.get('n1')!.record.meta.weight, wa);
  });

  it('Gossip 同步：权重 10 -> 80 传播到所有节点', () => {
    const [a, b, c] = buildCluster();
    a.updateMetadata('n1', { weight: 80 });

    // 反熵传播：a<->b, b<->c
    a.gossipWith(b);
    b.gossipWith(c);

    for (const list of [a, b, c]) {
      assert.equal(list.get('n1')!.record.meta.weight, 80);
    }
  });

  it('健康状态同步：dead 节点被排除', () => {
    const [a, b] = buildCluster();
    a.setHealth('n2', 'dead');
    a.gossipWith(b);
    assert.equal(b.get('n2')!.health, 'dead');
  });
});

describe('Router 属性筛选与路由', () => {
  it("zone=us-east 返回健康且权重最高的节点", () => {
    const [a, b] = buildCluster();
    // n1 权重 10 -> 80，超过 n2 的 50
    a.updateMetadata('n1', { weight: 80 });
    a.gossipWith(b);

    const router = new Router(b); // 从 b 的视角查询，验证 gossip 已生效
    const hit = router.route({ zone: 'us-east' });
    assert.ok(hit);
    assert.equal(hit.id, 'n1');
    assert.equal(hit.record.meta.weight, 80);
  });

  it('权重最高者不健康时，降级到次优健康节点', () => {
    const [ml] = buildCluster();
    ml.updateMetadata('n1', { weight: 80 });
    ml.setHealth('n1', 'dead');

    const router = new Router(ml);
    const hit = router.route({ zone: 'us-east' });
    assert.equal(hit!.id, 'n2');
  });

  it('异构标签筛选：labels.tier + zone 组合', () => {
    const [ml] = buildCluster();
    const router = new Router(ml);
    const subset = router.select({ zone: 'us-east', 'labels.tier': 'gold' });
    assert.deepEqual(subset.map((n) => n.id), ['n1']);
    assert.equal(router.route({ zone: 'eu-west' })!.id, 'n3');
    assert.equal(router.route({ zone: 'ap-south' }), null);
  });
});
