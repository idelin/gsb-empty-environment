/**
 * 运行样例：模拟异步服务器下的乐观更新全过程。
 *
 *   node --experimental-strip-types src/demo.ts   (Node >= 22.6)
 */
import { OptimisticStore } from './store.ts';

interface Doc {
  title: string;
  likes: number;
  tags: string[];
}

const store = new OptimisticStore<Doc>({ title: 'draft', likes: 0, tags: [] });

const show = (label: string): void => {
  console.log(`${label.padEnd(58)}`, JSON.stringify(store.state));
};

show('0. 初始状态');

// ---- A 乐观写，B 随后写同一字段 ----
store.applyOptimistic('op-A', 1, [{ kind: 'set', path: ['title'], value: 'A 的标题' }]);
show('1. A 乐观写 title=「A 的标题」(op-A@v1, pending)');

store.applyOptimistic('op-B', 1, [{ kind: 'set', path: ['title'], value: 'B 的标题' }]);
show('2. B 随后写 title=「B 的标题」(op-B@v1, pending)');

// ---- A 失败：只撤销 A 自己的效果，B 的写入保留 ----
store.reject('op-A', 1);
show('3. 服务器拒绝 op-A@v1 → 仅撤销 A，B 的标题保留');

// ---- 确认按 opId + version 匹配 ----
const mismatched = store.confirm('op-B', 99);
console.log('4. 错误版本的确认 confirm(op-B, 99) →', JSON.stringify(mismatched));
show('   状态不受影响');

store.confirm('op-B', 1);
show('5. 正确确认 confirm(op-B, 1) → op-B 落账');

// ---- 重复确认幂等 ----
const before = store.state;
const dup = store.confirm('op-B', 1);
console.log('6. 重复确认 confirm(op-B, 1) →', JSON.stringify(dup),
  '| 快照引用不变:', store.state === before);

// ---- 迟到的旧响应不回退新状态 ----
store.applyOptimistic('op-C', 1, [{ kind: 'set', path: ['likes'], value: 1 }]);
store.confirm('op-C', 1);
const lateReject = store.reject('op-C', 1);
console.log('7. 迟到的 reject(op-C, 1)（已确认）→', JSON.stringify(lateReject));
show('   likes 保持 1，未回退');

// ---- 版本过期：op-D 被拒后以 v2 重试，迟到的 v1 响应被忽略 ----
store.applyOptimistic('op-D', 1, [{ kind: 'set', path: ['tags', 0], value: 'v1-标签' }]);
store.reject('op-D', 1);
store.applyOptimistic('op-D', 2, [{ kind: 'set', path: ['tags', 0], value: 'v2-标签' }]);
const staleV1 = store.confirm('op-D', 1);
console.log('8. 迟到的 confirm(op-D, 1)（当前 v2）→', JSON.stringify(staleV1));
show('   v2 的写入不受影响');

// ---- 事务：读者看不到中间态 ----
const seen: string[] = [];
const unsubscribe = store.subscribe((snapshot) => seen.push(JSON.stringify(snapshot)));
store.transact((tx) => {
  tx.applyOptimistic('op-E', 1, [{ kind: 'set', path: ['likes'], value: 2 }]);
  tx.applyOptimistic('op-F', 1, [{ kind: 'set', path: ['tags', 1], value: 'tx' }]);
});
unsubscribe();
console.log('9. 事务内 2 个操作 → 读者收到通知次数:', seen.length, '（仅最终态 1 次）');
show('   事务发布后的状态');

// ---- 状态可回放 ----
const replayed = OptimisticStore.replay<Doc>({ title: 'draft', likes: 0, tags: [] },
  store.log.map((record) => ({ ...record })));
console.log('10. 从基础状态 + 操作日志重放 == 当前状态:',
  JSON.stringify(replayed) === JSON.stringify(store.state));
