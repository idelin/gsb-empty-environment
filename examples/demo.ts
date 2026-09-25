import { PolicyEnforcementPoint } from '../src/pep.ts';
import type { Policy } from '../src/policy.ts';

const policyV1: Policy = {
  version: 1,
  attributes: [
    { name: 'role', type: 'string' },
    { name: 'clearance', type: 'integer' },
    { name: 'mfaPassed', type: 'boolean' },
    { name: 'department', type: 'string' },
  ],
  rules: [
    {
      id: 'deny-intern',
      effect: 'deny',
      condition: { kind: 'in', attribute: 'role', values: ['intern'] },
      ttlSeconds: 30,
    },
    {
      id: 'allow-staff',
      effect: 'allow',
      condition: {
        kind: 'and',
        all: [
          { kind: 'in', attribute: 'role', values: ['admin', 'staff'] },
          { kind: 'int', attribute: 'clearance', op: 'ge', value: 3 },
          { kind: 'bool', attribute: 'mfaPassed', value: true },
        ],
      },
      ttlSeconds: 30,
    },
  ],
};

function show(label: string, result: { decision: string; fromCache: boolean }): void {
  const source = result.fromCache ? '缓存命中' : '重新求值';
  console.log(`${label.padEnd(52)} => ${result.decision}（${source}）`);
}

const pep = new PolicyEnforcementPoint(policyV1);
pep.attributes.set('alice', 'role', 'staff');
pep.attributes.set('alice', 'clearance', 5);
pep.attributes.set('alice', 'mfaPassed', true);
pep.attributes.set('alice', 'department', 'engineering');
pep.attributes.set('bob', 'role', 'staff');
pep.attributes.set('bob', 'clearance', 5);
pep.attributes.set('bob', 'mfaPassed', true);

console.log('--- 基本缓存 ---');
show('1. alice 首次访问 doc:read', pep.check('alice', 'doc:read'));
show('2. alice 再次访问 doc:read', pep.check('alice', 'doc:read'));

console.log('\n--- 跨主体 / 跨上下文不串用 ---');
show('3. bob 首次访问 doc:read（不借用 alice 的缓存）', pep.check('bob', 'doc:read'));
show('4. alice 首次访问 doc:write（新上下文）', pep.check('alice', 'doc:write'));

console.log('\n--- 未读取属性改变不失效 ---');
pep.attributes.set('alice', 'department', 'finance');
show('5. 修改 department（策略未读取）后再访问', pep.check('alice', 'doc:read'));

console.log('\n--- 已读取属性更正只失效相关条目 ---');
pep.attributes.set('alice', 'clearance', 1);
show('6. clearance 降为 1 后再访问', pep.check('alice', 'doc:read'));
show('7. bob 的 doc:read 条目不受影响', pep.check('bob', 'doc:read'));

console.log('\n--- 已读取属性删除必须失效 ---');
pep.attributes.set('alice', 'clearance', 5);
pep.check('alice', 'doc:read');
pep.attributes.delete('alice', 'mfaPassed');
show('8. 删除 mfaPassed 后再访问', pep.check('alice', 'doc:read'));

console.log('\n--- 规则版本变化不能沿用旧允许结果 ---');
pep.attributes.set('alice', 'mfaPassed', true);
show('9. 恢复 mfaPassed 后访问', pep.check('alice', 'doc:read'));
const policyV2: Policy = {
  ...policyV1,
  version: 2,
  rules: policyV1.rules.map((r) =>
    r.id === 'allow-staff'
      ? { ...r, condition: { kind: 'and', all: [
            { kind: 'in', attribute: 'role', values: ['admin', 'staff'] },
            { kind: 'int', attribute: 'clearance', op: 'ge', value: 10 },
            { kind: 'bool', attribute: 'mfaPassed', value: true },
          ] } }
      : r,
  ),
};
pep.setPolicy(policyV2);
show('10. 规则升到 v2（clearance 门槛 10）后访问', pep.check('alice', 'doc:read'));
