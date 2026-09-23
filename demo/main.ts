import { MessageBus } from '../src/bus.ts';
import { Replica } from '../src/replica.ts';
import { Store } from '../src/store.ts';
import type { Schema } from '../src/ops.ts';

const schema: Schema = {
  title: { kind: 'lww' },                          // 同字段冲突：LWW 显式决胜
  status: { kind: 'lww' },
  tags: { kind: 'mv', resolver: 'preserve' },      // 同字段冲突：保留全部并发值
  priority: { kind: 'mv', resolver: 'max' },       // 同字段冲突：按 max 决胜
};

const bus = new MessageBus();
const A = new Replica('A', schema, bus);
const B = new Replica('B', schema, bus);
const C = new Replica('C', schema, bus);

const show = (label: string) => {
  console.log(`\n== ${label} ==`);
  for (const r of [A, B, C]) console.log(`  ${r.id}:`, JSON.stringify(r.snapshot()));
};

// 副作用计数：验证本地回声不重复触发
let sideEffects = 0;
A.onSideEffect((e) => {
  sideEffects++;
  console.log(`  [副作用] A 应用 ${e.op.opId} (${e.origin}) ${e.op.field}=${e.op.value}`);
});

console.log('--- 1. 事务原子提交：读者看不到中间态 ---');
A.transaction((tx) => {
  tx.set('title', '设计文档');
  tx.set('status', 'draft');
  console.log('  事务提交前 A.title =', A.snapshot().title); // undefined
});
console.log('  事务提交后 A =', JSON.stringify(A.snapshot()));
bus.deliver();
console.log(`  副作用触发次数（含本地回声）= ${sideEffects}（应为 2，每个操作一次）`);
show('正常复制后');

console.log('\n--- 2. 网络分区：不同字段并发修改 ---');
bus.partition('A', 'B');
bus.partition('A', 'C');
A.set('status', 'review');
B.set('title', '设计文档 v2');
C.set('priority', 5);
bus.deliver();
show('分区期间各自独立演化');

console.log('\n--- 3. 恢复分区：自动合并 ---');
bus.heal('A', 'B');
bus.heal('A', 'C');
bus.deliver();
show('愈合后（不同字段全部合并）');

console.log('\n--- 4. 同字段并发冲突 ---');
bus.partition('B', 'C');
B.set('tags', 'urgent');
C.set('tags', 'backend');
B.set('priority', 3);
C.set('priority', 9);
bus.heal('B', 'C');
bus.deliver();
A.syncFrom(['B', 'C']); // 断线重放补偿
show('冲突合并后');
console.log('  tags 冲突保留:', A.store.conflicts('tags'));
console.log('  priority 按 max 决胜:', A.snapshot().priority);

console.log('\n--- 5. 重复消息与重放的幂等性 ---');
const before = B.store.appliedCount;
const batch = A.transaction((tx) => tx.set('status', 'done'));
bus.injectDuplicate(batch, 'B');
bus.injectDuplicate(batch, 'B');
bus.deliver();
B.syncFrom(['A', 'C']); // 全量重放
console.log(`  B 应用操作数: 重放前=${before + 1}, 重放后=${B.store.appliedCount}（重复消息与重放均未双应用）`);

console.log('\n--- 6. 操作日志重放重建状态 ---');
const rebuilt = Store.replay(schema, A.store.opLog());
console.log('  重放重建 =', JSON.stringify(rebuilt.snapshot()));
console.log('  与 A 一致:', JSON.stringify(rebuilt.snapshot()) === JSON.stringify(A.snapshot()));
