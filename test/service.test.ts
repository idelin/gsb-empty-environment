import { test } from 'node:test';
import assert from 'node:assert/strict';
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

function makeService(ttlMs = 60_000) {
  let now = 0;
  const service = new AuthorizationService({ policy: policyV1, ttlMs, now: () => now });
  return { service, advance: (ms: number) => { now += ms; } };
}

test('判定结果按 (策略, 主体, 上下文) 缓存，跨主体和上下文不串用', () => {
  const { service } = makeService();
  service.setSubjectAttribute('alice', 'role', 'editor');
  service.setSubjectAttribute('bob', 'role', 'editor');

  assert.equal(service.authorize('alice', 'web').cache, 'miss');
  assert.equal(service.authorize('alice', 'web').cache, 'hit');
  assert.equal(service.authorize('bob', 'web').cache, 'miss', '属性相同也不能复用其他主体的条目');
  assert.equal(service.authorize('alice', 'mobile').cache, 'miss', '同一主体换上下文也不能复用');
});

test('缓存条目记录实际读取的属性', () => {
  const { service } = makeService();
  service.setSubjectAttribute('alice', 'role', 'editor');
  const result = service.authorize('alice', 'web');
  assert.equal(result.cache, 'miss');
  assert.deepEqual(
    [...(result.entry?.reads.keys() ?? [])],
    ['subject:alice:role'],
    'allow-staff 命中后短路，department/hour 不会被读取',
  );
});

test('未读取属性改变不需失效', () => {
  const { service } = makeService();
  service.setSubjectAttribute('alice', 'role', 'editor');
  service.authorize('alice', 'web');
  assert.equal(service.cacheSize, 1);

  service.setSubjectAttribute('alice', 'department', 'eng');
  assert.equal(service.cacheSize, 1, 'department 未被 alice 的条目读取，不应失效');
  assert.equal(service.authorize('alice', 'web').cache, 'hit');
});

test('属性更正仅失效实际读取过该属性的条目', () => {
  const { service } = makeService();
  service.setSubjectAttribute('carol', 'role', 'viewer');
  service.setSubjectAttribute('carol', 'department', 'eng');
  service.setSubjectAttribute('dave', 'role', 'viewer');
  service.setSubjectAttribute('dave', 'department', 'ops');
  service.setContextAttribute('morning', 'hour', 10);

  assert.equal(service.authorize('carol', 'morning').decision, 'allow');
  assert.equal(service.authorize('dave', 'morning').decision, 'allow');
  assert.equal(service.cacheSize, 2);

  service.setSubjectAttribute('carol', 'department', 'finance');
  assert.equal(service.cacheSize, 1, '只有 carol 的条目失效');
  assert.equal(service.authorize('dave', 'morning').cache, 'hit');
  const carolAgain = service.authorize('carol', 'morning');
  assert.equal(carolAgain.cache, 'miss');
  assert.equal(carolAgain.decision, 'deny', '更正后按新属性重新判定');
});

test('已读取属性删除必须失效；删除不存在的属性不失效', () => {
  const { service } = makeService();
  service.setSubjectAttribute('carol', 'role', 'viewer');
  service.setSubjectAttribute('carol', 'department', 'eng');
  service.setContextAttribute('morning', 'hour', 10);
  service.authorize('carol', 'morning');
  assert.equal(service.cacheSize, 1);

  service.deleteSubjectAttribute('carol', 'nonexistent');
  assert.equal(service.cacheSize, 1, '删除本就不存在的属性不影响缓存');

  service.deleteSubjectAttribute('carol', 'department');
  assert.equal(service.cacheSize, 0, '已读取的 department 被删除，条目必须失效');
  assert.equal(service.authorize('carol', 'morning').decision, 'deny');
});

test('规则版本变化不能沿用旧允许结果', () => {
  const { service } = makeService();
  service.setSubjectAttribute('alice', 'role', 'editor');
  assert.equal(service.authorize('alice', 'web').decision, 'allow');
  assert.equal(service.authorize('alice', 'web').cache, 'hit');

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
  assert.equal(service.cacheSize, 0, '版本变化后旧条目全部作废');

  const after = service.authorize('alice', 'web');
  assert.equal(after.cache, 'miss');
  assert.equal(after.decision, 'deny', 'editor 不再属于允许集合，旧允许结果不得沿用');
});

test('规则变更但不递增版本号会被拒绝', () => {
  const { service } = makeService();
  const sameVersion: Policy = {
    ...policyV1,
    rules: [
      { id: 'allow-all', effect: 'allow', when: { op: 'in', attr: 'subject.role', values: ['admin', 'editor', 'viewer'] } },
    ],
  };
  assert.throws(() => service.updatePolicy(sameVersion), /递增版本号/);
});

test('时间失效点：过期后重新判定', () => {
  const { service, advance } = makeService(100);
  service.setSubjectAttribute('alice', 'role', 'editor');
  assert.equal(service.authorize('alice', 'web').cache, 'miss');
  advance(50);
  assert.equal(service.authorize('alice', 'web').cache, 'hit');
  advance(60);
  assert.equal(service.authorize('alice', 'web').cache, 'miss', '超过时间失效点后必须重新判定');
});

test('拒绝优先与默认拒绝', () => {
  const { service } = makeService();
  service.setSubjectAttribute('mallory', 'role', 'guest');
  service.setSubjectAttribute('mallory', 'department', 'eng');
  service.setContextAttribute('morning', 'hour', 10);
  assert.equal(service.authorize('mallory', 'morning').decision, 'deny', 'deny 规则优先于 allow');

  service.setSubjectAttribute('nobody', 'role', 'viewer');
  assert.equal(service.authorize('nobody', 'morning').decision, 'deny', '无规则命中时默认拒绝');
});
