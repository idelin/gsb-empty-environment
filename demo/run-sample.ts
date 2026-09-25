import { AuthorizationService } from '../src/service.ts';
import type { Policy } from '../src/engine.ts';

const policyV1: Policy = {
  id: 'docs',
  version: 1,
  rules: [
    { id: 'deny-guest', effect: 'deny', when: { op: 'in', attr: 'subject.role', values: ['guest'] } },
    { id: 'allow-staff', effect: 'allow', when: { op: 'in', attr: 'subject.role', values: ['admin', 'editor'] } },
    {
      id: 'allow-business-hours',
      effect: 'allow',
      when: {
        op: 'and',
        all: [
          { op: 'in', attr: 'subject.department', values: ['eng', 'ops'] },
          { op: 'ge', attr: 'context.hour', value: 9 },
          { op: 'le', attr: 'context.hour', value: 18 },
        ],
      },
    },
  ],
};

const service = new AuthorizationService({ policy: policyV1, ttlMs: 200 });

function show(label: string, subject: string, context: string): void {
  const result = service.authorize(subject, context);
  const reads = result.entry ? ` 读取属性=[${[...result.entry.reads.keys()].join(', ')}] 失效点=+${result.entry.expiresAt - Date.now()}ms内` : '';
  console.log(`  [${result.cache}] ${subject}@${context} -> ${result.decision}${reads}`);
  if (label) console.log(`    ^ ${label}`);
}

console.log('== 1. 初始判定：缓存记录实际读取属性 ==');
service.setSubjectAttribute('alice', 'role', 'editor');
service.setSubjectAttribute('carol', 'role', 'viewer');
service.setSubjectAttribute('carol', 'department', 'eng');
service.setContextAttribute('morning', 'hour', 10);
show('alice 命中 allow-staff 后短路，只读了 role', 'alice', 'morning');
show('carol 走 allow-business-hours，读了 role/department/hour', 'carol', 'morning');
show('alice 第二次命中缓存', 'alice', 'morning');

console.log('\n== 2. 跨主体和上下文不串用 ==');
service.setSubjectAttribute('bob', 'role', 'editor');
show('bob 属性与 alice 相同，仍是 miss', 'bob', 'morning');
show('alice 换上下文 evening，仍是 miss', 'alice', 'evening');

console.log('\n== 3. 未读取属性改变不需失效 ==');
service.setSubjectAttribute('alice', 'level', 7);
console.log(`  修改 alice 从未被读取的 level 后，缓存条目数=${service.cacheSize}（不变）`);
show('alice 仍然命中缓存', 'alice', 'morning');

console.log('\n== 4. 属性更正仅失效相关条目 ==');
service.setSubjectAttribute('carol', 'department', 'finance');
console.log(`  更正 carol.department=finance 后，缓存条目数=${service.cacheSize}（只失效 carol@morning）`);
show('carol 重新判定为 deny', 'carol', 'morning');
show('alice 不受影响仍命中', 'alice', 'morning');

console.log('\n== 5. 已读取属性删除必须失效 ==');
service.deleteSubjectAttribute('alice', 'role');
console.log(`  删除 alice.role（已被读取）后，缓存条目数=${service.cacheSize}`);
show('alice 重新判定为 deny', 'alice', 'morning');

console.log('\n== 6. 规则版本变化不能沿用旧允许结果 ==');
service.setSubjectAttribute('alice', 'role', 'editor');
show('alice 恢复 role 后重新允许', 'alice', 'morning');
const policyV2: Policy = {
  ...policyV1,
  version: 2,
  rules: policyV1.rules.map((rule) =>
    rule.id === 'allow-staff'
      ? { ...rule, when: { op: 'in', attr: 'subject.role', values: ['admin'] } }
      : rule,
  ),
};
service.updatePolicy(policyV2);
console.log(`  策略升级到 v${service.policyVersion}（allow-staff 仅含 admin），缓存条目数=${service.cacheSize}`);
show('旧允许结果不得沿用，alice 变为 deny', 'alice', 'morning');

console.log('\n== 7. 时间失效点 ==');
service.setSubjectAttribute('alice', 'role', 'admin');
show('alice 作为 admin 允许', 'alice', 'morning');
await new Promise((resolve) => setTimeout(resolve, 250));
show('250ms 后超过 200ms 失效点，重新判定', 'alice', 'morning');
