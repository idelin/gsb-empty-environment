import { IncrementalTopo, CycleError } from '../src/incremental-topo.ts';

const g = new IncrementalTopo();

console.log('== 建立初始依赖链 1->2->3->4 ==');
await g.addEdge(1, 2);
await g.addEdge(2, 3);
await g.addEdge(3, 4);
console.log('拓扑序:', g.getOrder().join(' -> '));

console.log('\n== 尝试增加边 4->2（成环，应被拒绝）==');
try {
  await g.addEdge(4, 2);
  console.log('意外成功！');
} catch (e) {
  if (e instanceof CycleError) {
    console.log('检测到死锁环路:', e.cycle.join(' -> '));
  }
}
console.log('拓扑序保持不变:', g.getOrder().join(' -> '));

console.log('\n== 增加边 1->4（合法，局部重排）==');
await g.addEdge(1, 4);
console.log('拓扑序:', g.getOrder().join(' -> '));
console.log('拓扑单调性校验:', g.isValid() ? '通过' : '失败');

console.log('\n== 并发加边演示 ==');
const results = await Promise.allSettled([
  g.addEdge(2, 4),
  g.addEdge(4, 1),
  g.addEdge(1, 3),
]);
for (const r of results) {
  if (r.status === 'fulfilled') {
    console.log('接受');
  } else {
    console.log('拒绝: ' + (r.reason as Error).message);
  }
}
console.log('最终拓扑序:', g.getOrder().join(' -> '));
