import { GossipCluster } from '../src/gossip.ts';
import { Router } from '../src/router.ts';

// 运行样例：3 节点集群，动态更新权重 -> Gossip 同步 -> 按属性路由

const cluster = new GossipCluster();

cluster.addNode('node-a', '10.0.0.1:7946', {
  zone: 'us-east', weight: 10, role: 'worker',
  labels: { env: 'prod', tier: 'compute' },
  attributes: { gpu: false },
});
cluster.addNode('node-b', '10.0.0.2:7946', {
  zone: 'us-east', weight: 50, role: 'worker',
  labels: { env: 'prod', tier: 'compute' },
  attributes: { gpu: true },
});
cluster.addNode('node-c', '10.0.0.3:7946', {
  zone: 'us-west', weight: 90, role: 'leader',
  labels: { env: 'prod', tier: 'control' },
  attributes: { gpu: false },
});

const routerOnA = new Router(cluster.get('node-a'));

console.log('=== 1. 初始状态：查询 zone=us-east ===');
console.log(routerOnA.query('zone=us-east'));

console.log('\n=== 2. node-a 负载权重动态更新：10 -> 80 ===');
cluster.get('node-a').updateLocalMetadata({ weight: 80 });
console.log('node-a 本地视图:', cluster.get('node-a').get('node-a')!.metadata);
console.log('gossip 前 node-b 眼中的 node-a 权重:', cluster.get('node-b').get('node-a')!.metadata.weight);

const rounds = cluster.converge();
console.log(`gossip 收敛轮数: ${rounds}`);
console.log('gossip 后 node-b 眼中的 node-a 权重:', cluster.get('node-b').get('node-a')!.metadata.weight);
console.log('gossip 后 node-c 眼中的 node-a 权重:', cluster.get('node-c').get('node-a')!.metadata.weight);

console.log('\n=== 3. 路由查询 zone=us-east（返回健康且权重最高的节点）===');
const best = routerOnA.route('zone=us-east');
console.log(best);
console.assert(best?.nodeId === 'node-a' && best.weight === 80, '应路由到权重 80 的 node-a');

console.log('\n=== 4. 组合筛选：zone=us-east 且 weight>=40 且角色非 leader ===');
console.log(routerOnA.query('zone=us-east weight>=40 role!=leader'));

console.log('\n=== 5. 节点 y 失联后，路由视图自动将其剔除 ===');
// 模拟时钟推进使远端心跳过期：重建一个带可控时钟的集群演示
let tick = 1_000;
const cluster2 = new GossipCluster({ now: () => tick, suspectAfterMs: 100, deadAfterMs: 500 });
cluster2.addNode('x', '10.1.0.1:7946', { zone: 'us-east', weight: 90, role: 'worker', labels: {}, attributes: {} });
cluster2.addNode('y', '10.1.0.2:7946', { zone: 'us-east', weight: 60, role: 'worker', labels: {}, attributes: {} });
tick += 1_000; // 所有远端心跳过期 -> dead
const routerOnX = new Router(cluster2.get('x'));
console.log(routerOnX.query('zone=us-east'));

console.log('\nDemo 完成 ✔');
