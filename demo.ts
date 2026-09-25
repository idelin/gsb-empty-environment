/** 运行样例：需求中的关键场景端到端演示。 */
import { PolicyEngine } from './src/engine.ts';
import { verifyAgainstEnumeration } from './src/verify.ts';

const show = (label: string, v: unknown) => console.log(label.padEnd(34), JSON.stringify(v));

console.log('=== 1. 半开边界：[0,5) allow 与 [5,10) deny ===');
const eng = new PolicyEngine('deny'); // 默认策略：deny
eng.applyBatch([
  { type: 'insert', rule: { id: 'A', start: 0, end: 5, priority: 0, version: 1, effect: 'allow',
    condition: { kind: 'in', attr: 'role', values: [1, 2] } } },
  { type: 'insert', rule: { id: 'B', start: 5, end: 10, priority: 0, version: 1, effect: 'deny',
    condition: { kind: 'const', value: true } } },
]);
show('query(4, {role:1})', eng.query(4, { role: 1 }));
show('query(4, {role:9})', eng.query(4, { role: 9 }));
show('query(5)  <-- 边界点', eng.query(5));

console.log('\n=== 2. 段结构：不重叠判定段 + 决策链共享 ===');
for (const s of eng.getSegments())
  show(`  段 [${s.start}, ${s.end})`, { chainId: s.chain.id, entries: s.chain.entries.map((e) => e.ruleId) });
show('条件共享池大小 / 决策链池大小', [eng.condPoolSize, eng.chainPoolSize]);

console.log('\n=== 3. 端点改变：A 改为 [0,7) v2，撤回旧证据 ===');
const rep = eng.insert({ id: 'A', start: 0, end: 7, priority: 0, version: 2, effect: 'allow',
  condition: { kind: 'in', attr: 'role', values: [1, 2] } });
show('触及的段', rep.touchedSegments);
show('触及的决策节点', rep.touchedDecisionNodes);
show('撤回 / 插入', [rep.retracted, rep.inserted]);
show('query(6, {role:1}) 新证据生效', eng.query(6, { role: 1 }));

console.log('\n=== 4. 批量原子更新 + 撤销后恢复默认 ===');
eng.applyBatch([
  { type: 'delete', id: 'A' },
  { type: 'delete', id: 'B' },
]);
show('query(5) 撤销后 => 默认策略', eng.query(5));
try {
  eng.applyBatch([
    { type: 'insert', rule: { id: 'C', start: 0, end: 3, priority: 0, version: 1, effect: 'allow',
      condition: { kind: 'const', value: true } } },
    { type: 'delete', id: '不存在的规则' },
  ]);
} catch (e) {
  show('非法批量被拒绝', (e as Error).message);
}
show('状态未变(规则数)', eng.snapshotRules().length);

console.log('\n=== 5. 相邻同结果不同来源：证据保留 ===');
eng.applyBatch([
  { type: 'insert', rule: { id: 'D', start: 0, end: 5, priority: 0, version: 1, effect: 'deny',
    condition: { kind: 'const', value: true } } },
  { type: 'insert', rule: { id: 'E', start: 5, end: 10, priority: 0, version: 1, effect: 'deny',
    condition: { kind: 'const', value: true } } },
]);
show('段数(同效果不同源不合并)', eng.getSegments().length);
show('query(4).source / query(5).source', [eng.query(4).source, eng.query(5).source]);

console.log('\n=== 6. 有限时间格 × 属性域全枚举对照 ===');
const verify = verifyAgainstEnumeration(eng, {
  timeMin: 0, timeMax: 12, domains: { role: [0, 1, 2, 9] },
});
show('枚举点数 / 不一致数', [verify.checked, verify.mismatches.length]);
