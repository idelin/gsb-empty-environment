/**
 * 运行样例：
 *   node examples/demo.ts
 *
 * 场景：三节点集群，n1 负载权重由 10 动态调整为 80，
 * 通过 Gossip 同步后，路由查询 zone=us-east 返回权重最高的健康节点。
 */
import { MemberList } from '../src/memberlist.ts';
import { Router } from '../src/router.ts';

const [a, b, c] = [new MemberList('n1'), new MemberList('n2'), new MemberList('n3')];
for (const list of [a, b, c]) {
  list.addNode('n1', '10.0.0.1:7000', { zone: 'us-east', weight: 10, role: 'leader', labels: { tier: 'gold' } });
  list.addNode('n2', '10.0.0.2:7000', { zone: 'us-east', weight: 50, role: 'follower', labels: {} });
  list.addNode('n3', '10.0.0.3:7000', { zone: 'eu-west', weight: 90, role: 'follower', labels: {} });
}

const routerA = new Router(a);
console.log('[1] 初始路由 zone=us-east ->', routerA.route({ zone: 'us-east' })?.id, '(weight=50)');

// 动态更新：n1 权重 10 -> 80（在节点 a 的视图上发起）
const version = a.updateMetadata('n1', { weight: 80 });
console.log(`[2] n1 权重更新 10 -> 80 (version=${version})`);

// Gossip 反熵传播到全集群
a.gossipWith(b);
b.gossipWith(c);
console.log('[3] Gossip 同步完成，各节点视图中的 n1.weight =',
  [a, b, c].map((l) => l.get('n1')!.record.meta.weight).join(', '));

// 从任意节点视角路由查询
for (const [name, list] of [['a', a], ['b', b], ['c', c]] as const) {
  const hit = new Router(list).route({ zone: 'us-east' });
  console.log(`[4] 节点 ${name} 路由 zone=us-east -> ${hit!.id} (weight=${hit!.record.meta.weight})`);
}

// 故障降级：n1 宕机后，流量切到次优健康节点 n2
a.setHealth('n1', 'dead');
a.gossipWith(b);
console.log('[5] n1 宕机后，节点 b 路由 zone=us-east ->', new Router(b).route({ zone: 'us-east' })?.id);
