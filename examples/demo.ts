import { Registry } from '../src/registry.ts';
import { Store } from '../src/store.ts';
import { serializeStore } from '../src/serialize.ts';
import { RestoreError } from '../src/errors.ts';

// 1. 注册计算定义（函数本体只存在于注册表，绝不序列化）
const registry = new Registry()
  .register({ id: 'add', version: 1, compute: ([a, b]) => (a as number) + (b as number) })
  .register({ id: 'mul', version: 1, compute: ([a, b]) => (a as number) * (b as number) })
  .register({
    id: 'acc',
    version: 1,
    monotonic: true, // 允许指向自身的单调反馈环
    initial: 0,
    compute: ([prev, x]) => (prev as number) + (x as number),
  });

// 2. 事务建图：状态节点 + 计算节点（含合法单调环 total→total）
const addDef = registry.get('add', 1)!;
const mulDef = registry.get('mul', 1)!;
const accDef = registry.get('acc', 1)!;

const store = new Store(registry);
store
  .begin()
  .defineState('a', 1)
  .defineState('b', 2)
  .defineComputed('sum', addDef, ['a', 'b'])
  .defineComputed('product', mulDef, ['a', 'b'])
  .defineComputed('total', accDef, ['total', 'a'])
  .commit();

store.subscribe('sum', (v, prev) => console.log(`  [订阅回调] sum: ${String(prev)} -> ${String(v)}`));

console.log('== 初始提交 ==');
console.log(`  a=${String(store.get('a'))} b=${String(store.get('b'))} sum=${String(store.get('sum'))} product=${String(store.get('product'))} total=${String(store.get('total'))}`);

// 3. 原子事务：读者看不到中间态
console.log('== 原子事务 set(a=10), set(b=20) ==');
store.begin().set('a', 10).set('b', 20).commit();
console.log(`  sum=${String(store.get('sum'))} product=${String(store.get('product'))} total=${String(store.get('total'))}`);

// 4. 序列化（带完整性摘要），函数不落盘
const file = serializeStore(store, { integrity: true });
console.log('== 序列化快照 ==');
console.log(JSON.stringify(file, null, 2));

// 5. 操作日志回放
const replayed = Store.replay(store.ops(), registry);
console.log(`== 操作日志回放 == sum=${String(replayed.get('sum'))} total=${String(replayed.get('total'))}`);

// 6. 原子恢复：完整校验通过后才发布，订阅回调只在发布后触发一次
const restored = new Store(registry);
restored.subscribe('sum', (v) => console.log(`  [订阅回调] 恢复后 sum=${String(v)}`));
restored.restore(JSON.parse(JSON.stringify(file)));
console.log(`== 恢复 == sum=${String(restored.get('sum'))} total=${String(restored.get('total'))}（单调节点保持携带值，不重复累积）`);

// 7. 篡改依赖边 → 完整性校验拒绝，旧状态保持可读
const tampered = JSON.parse(JSON.stringify(file)) as typeof file;
for (const n of tampered.nodes) {
  if (n.id === 'sum') (n.deps as string[])[0] = 'b';
}
try {
  restored.restore(tampered);
} catch (e) {
  console.log(`== 篡改依赖边被拒绝 == ${(e as RestoreError).code}: ${(e as Error).message}`);
}
console.log(`  旧状态仍可读: sum=${String(restored.get('sum'))}`);

// 8. 版本不符 / 未知定义 / 非法环 / schema 版本不兼容
const badVersion = JSON.parse(JSON.stringify(file)) as typeof file;
delete (badVersion as { integrity?: string }).integrity;
for (const n of badVersion.nodes) {
  if (n.kind === 'computed') (n.def as { version: number }).version = 99;
}
try {
  restored.restore(badVersion);
} catch (e) {
  console.log(`== 定义版本不符被拒绝 == ${(e as RestoreError).code}`);
}

try {
  restored.restore({
    schemaVersion: 1,
    nodes: [
      { id: 'a', kind: 'state', value: 1 },
      { id: 'x', kind: 'computed', def: { id: 'add', version: 1 }, deps: ['y', 'a'] },
      { id: 'y', kind: 'computed', def: { id: 'add', version: 1 }, deps: ['x', 'a'] },
    ],
  });
} catch (e) {
  console.log(`== 非法求值环被拒绝 == ${(e as RestoreError).code}: ${(e as Error).message}`);
}

try {
  restored.restore({ schemaVersion: 999, nodes: [] });
} catch (e) {
  console.log(`== schema 版本不兼容被拒绝 == ${(e as RestoreError).code}: ${(e as Error).message}`);
}
