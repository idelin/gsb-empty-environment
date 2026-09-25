import assert from 'node:assert/strict';
import { test } from 'node:test';
import { PolicyEnforcementPoint } from '../src/pep.ts';
import type { Policy } from '../src/policy.ts';
import { validatePolicy } from '../src/policy.ts';

/** 基础策略：显式声明 4 个属性；两条规则分别只读其中一部分 */
function makePolicy(version: number, opts: { requiredClearance?: number; ttlSeconds?: number } = {}): Policy {
  const requiredClearance = opts.requiredClearance ?? 3;
  const ttlSeconds = opts.ttlSeconds ?? 60;
  return {
    version,
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
        ttlSeconds,
      },
      {
        id: 'allow-staff',
        effect: 'allow',
        condition: {
          kind: 'and',
          all: [
            { kind: 'in', attribute: 'role', values: ['admin', 'staff'] },
            { kind: 'int', attribute: 'clearance', op: 'ge', value: requiredClearance },
            { kind: 'bool', attribute: 'mfaPassed', value: true },
          ],
        },
        ttlSeconds,
      },
    ],
  };
}

function makePep(policyOpts?: { requiredClearance?: number; ttlSeconds?: number }, now?: () => number) {
  const pep = new PolicyEnforcementPoint(makePolicy(1, policyOpts), now ? { now } : {});
  pep.attributes.set('alice', 'role', 'staff');
  pep.attributes.set('alice', 'clearance', 5);
  pep.attributes.set('alice', 'mfaPassed', true);
  pep.attributes.set('alice', 'department', 'engineering');
  pep.attributes.set('bob', 'role', 'staff');
  pep.attributes.set('bob', 'clearance', 5);
  pep.attributes.set('bob', 'mfaPassed', true);
  return pep;
}

test('首次求值后缓存命中，不重复求值', () => {
  const pep = makePep();
  const first = pep.check('alice', 'doc:read');
  const second = pep.check('alice', 'doc:read');
  assert.equal(first.decision, 'allow');
  assert.equal(first.fromCache, false);
  assert.equal(second.decision, 'allow');
  assert.equal(second.fromCache, true);
});

test('缓存条目记录实际读取属性、规则版本与时间失效点', () => {
  let now = 1_000_000;
  const pep = makePep({ ttlSeconds: 30 }, () => now);
  pep.check('alice', 'doc:read');
  const entry = pep.cache.get({ subjectId: 'alice', contextId: 'doc:read' });
  assert.ok(entry);
  assert.deepEqual(
    entry.reads.map((r) => r.attribute).sort(),
    ['clearance', 'mfaPassed', 'role'],
  );
  assert.equal(entry.ruleVersion, 1);
  assert.equal(entry.expiresAt, 1_000_000 + 30_000);
});

test('跨主体不串用：相同属性也不共享缓存条目', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  const bob = pep.check('bob', 'doc:read');
  assert.equal(bob.fromCache, false);
  assert.equal(bob.decision, 'allow');
});

test('跨上下文不串用：同一主体不同上下文各自缓存', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  const write = pep.check('alice', 'doc:write');
  assert.equal(write.fromCache, false);
});

test('未读取属性改变不失效：department 修改后仍命中缓存', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  pep.attributes.set('alice', 'department', 'finance'); // 策略从未读取
  const again = pep.check('alice', 'doc:read');
  assert.equal(again.fromCache, true);
  assert.equal(again.decision, 'allow');
});

test('已读取属性更正只失效相关条目：clearance 修改后重估，其他条目保留', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  pep.check('alice', 'doc:write');
  pep.check('bob', 'doc:read');
  pep.attributes.set('alice', 'clearance', 1); // 不再满足 >= 3
  const aliceRead = pep.check('alice', 'doc:read');
  const aliceWrite = pep.check('alice', 'doc:write');
  const bobRead = pep.check('bob', 'doc:read');
  assert.equal(aliceRead.fromCache, false);
  assert.equal(aliceRead.decision, 'deny');
  assert.equal(aliceWrite.fromCache, false);
  assert.equal(bobRead.fromCache, true); // 其他主体不受影响
  assert.equal(bobRead.decision, 'allow');
});

test('已读取属性删除必须失效', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  pep.attributes.delete('alice', 'mfaPassed');
  const again = pep.check('alice', 'doc:read');
  assert.equal(again.fromCache, false);
  assert.equal(again.decision, 'deny');
});

test('规则版本变化不能沿用旧允许结果', () => {
  const pep = makePep();
  const before = pep.check('alice', 'doc:read');
  assert.equal(before.decision, 'allow');
  // 新版本把 clearance 门槛提高到 10，alice 只有 5
  pep.setPolicy(makePolicy(2, { requiredClearance: 10 }));
  const after = pep.check('alice', 'doc:read');
  assert.equal(after.fromCache, false);
  assert.equal(after.ruleVersion, 2);
  assert.equal(after.decision, 'deny');
});

test('时间失效点：超过 TTL 后缓存失效并重新求值', () => {
  let now = 0;
  const pep = makePep({ ttlSeconds: 5 }, () => now);
  const first = pep.check('alice', 'doc:read');
  assert.equal(first.fromCache, false);
  now = 4_999;
  assert.equal(pep.check('alice', 'doc:read').fromCache, true);
  now = 5_000;
  const expired = pep.check('alice', 'doc:read');
  assert.equal(expired.fromCache, false);
  assert.equal(expired.decision, 'allow');
});

test('deny 规则优先：显式拒绝先于允许', () => {
  const pep = makePep();
  pep.attributes.set('carol', 'role', 'intern');
  pep.attributes.set('carol', 'clearance', 9);
  pep.attributes.set('carol', 'mfaPassed', true);
  const result = pep.check('carol', 'doc:read');
  assert.equal(result.decision, 'deny');
});

test('默认拒绝：未命中任何规则返回 deny', () => {
  const pep = makePep();
  pep.attributes.set('dave', 'role', 'contractor');
  const result = pep.check('dave', 'doc:read');
  assert.equal(result.decision, 'deny');
});

test('策略只读显式属性：条件引用未声明属性直接报错', () => {
  const bad: Policy = {
    version: 1,
    attributes: [{ name: 'role', type: 'string' }],
    rules: [
      {
        id: 'r1',
        effect: 'allow',
        condition: { kind: 'in', attribute: 'ssn', values: ['x'] },
        ttlSeconds: 10,
      },
    ],
  };
  assert.throws(() => validatePolicy(bad), /未在策略中显式声明/);
});

test('整数条件拒绝非整数比较值', () => {
  const bad: Policy = {
    version: 1,
    attributes: [{ name: 'clearance', type: 'integer' }],
    rules: [
      {
        id: 'r1',
        effect: 'allow',
        condition: { kind: 'int', attribute: 'clearance', op: 'ge', value: 2.5 },
        ttlSeconds: 10,
      },
    ],
  };
  assert.throws(() => validatePolicy(bad), /必须是整数/);
});

test('布尔运算：or / not 组合生效', () => {
  const policy: Policy = {
    version: 1,
    attributes: [
      { name: 'role', type: 'string' },
      { name: 'suspended', type: 'boolean' },
    ],
    rules: [
      {
        id: 'allow-active-privileged',
        effect: 'allow',
        condition: {
          kind: 'and',
          all: [
            { kind: 'not', condition: { kind: 'bool', attribute: 'suspended', value: true } },
            { kind: 'in', attribute: 'role', values: ['admin', 'auditor'] },
          ],
        },
        ttlSeconds: 10,
      },
    ],
  };
  const pep = new PolicyEnforcementPoint(policy);
  pep.attributes.set('eve', 'role', 'auditor');
  pep.attributes.set('eve', 'suspended', false);
  assert.equal(pep.check('eve', 'ctx').decision, 'allow');
  pep.attributes.set('eve', 'suspended', true);
  assert.equal(pep.check('eve', 'ctx').decision, 'deny');
});

test('写入未声明的属性被拒绝（属性集合封闭）', () => {
  const pep = makePep();
  assert.throws(() => pep.attributes.set('alice', 'ssn', '123'), /未在策略中显式声明/);
});

test('属性写入相同值不触发失效', () => {
  const pep = makePep();
  pep.check('alice', 'doc:read');
  pep.attributes.set('alice', 'clearance', 5); // 与当前值相同
  assert.equal(pep.check('alice', 'doc:read').fromCache, true);
});
