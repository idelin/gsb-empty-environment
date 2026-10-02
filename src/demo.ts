/**
 * 运行样例：模拟两个节点的并发操作与因果合并。
 * 验收场景：A 删除根键 user_profile，B 并发修改 user_profile.age = 25，
 * 合并后 Add-Wins 判定更新优先 → user_profile 及 age 字段保留。
 */
import { AWLWWMap } from './awlwwmap.ts';

// 1) 初始状态：两个副本同步于同一份数据
const seed = new AWLWWMap('seed');
seed.setField('user_profile', 'name', 'Ada', 1000);
seed.setField('user_profile', 'age', 30, 1001);

const nodeA = new AWLWWMap('nodeA');
const nodeB = new AWLWWMap('nodeB');
nodeA.merge(seed);
nodeB.merge(seed);

console.log('初始状态（A/B 一致）:', JSON.stringify(nodeA.toObject()));

// 2) 并发操作（双方均未观察到对方）
nodeA.remove('user_profile'); // A：删除根键
nodeB.setField('user_profile', 'age', 25, 2000); // B：并发修改子字段

console.log('A 本地（删除后）     :', JSON.stringify(nodeA.toObject()));
console.log('B 本地（更新后）     :', JSON.stringify(nodeB.toObject()));

// 3) 因果合并
nodeA.merge(nodeB);
nodeB.merge(nodeA);

console.log('合并后 A            :', JSON.stringify(nodeA.toObject()));
console.log('合并后 B            :', JSON.stringify(nodeB.toObject()));

// 4) 验收断言
const ok =
  nodeA.has('user_profile') &&
  nodeA.getField('user_profile', 'age') === 25 &&
  nodeA.getField('user_profile', 'name') === 'Ada' &&
  JSON.stringify(nodeA.toObject()) === JSON.stringify(nodeB.toObject());

console.log(ok ? '✅ 验收通过：更新优先，user_profile 及 age=25 被保留' : '❌ 验收失败');
process.exit(ok ? 0 : 1);
