import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AttributeStore, audit, boolAttr, evaluate, format, intAttr, limit, mask,
  reinstateRule, setAttr, withdrawRule,
  type Policy, type Rule,
} from '../src/index.ts';

const attrs = new AttributeStore({
  clearance: intAttr(3),
  department: setAttr(['finance', 'engineering']),
  mfaEnabled: boolAttr(true),
});

const policy: Policy = {
  rules: [
    {
      id: 'permit-analyst',
      effect: 'permit',
      condition: { op: 'intCmp', attr: 'clearance', cmp: '>=', value: 2 },
      obligations: [
        limit('maxRows', 'max', 1000, 'permit-analyst'),
        mask(['salary'], 'permit-analyst'),
        audit(['read'], 'permit-analyst'),
      ],
    },
    {
      id: 'permit-finance',
      effect: 'permit',
      condition: { op: 'inSet', attr: 'department', value: 'finance' },
      obligations: [
        limit('maxRows', 'max', 100, 'permit-finance'),
        mask(['ssn'], 'permit-finance'),
        format('json', 'permit-finance'),
      ],
    },
    {
      id: 'deny-no-mfa',
      effect: 'deny',
      condition: { op: 'boolIs', attr: 'mfaEnabled', value: false },
      obligations: [audit(['deny-no-mfa'], 'deny-no-mfa')],
    },
  ],
};

test('允许判定伴随义务：合并各义务专用代数', () => {
  const decision = evaluate(policy, attrs);
  assert.equal(decision.effect, 'permit');
  assert.deepEqual(decision.applicableRules, ['permit-analyst', 'permit-finance']);
  assert.equal(decision.merged.status, 'ok');
  if (decision.merged.status !== 'ok') return;
  // 限额取更严格值
  assert.equal(decision.merged.solution.bounds['maxRows']!.hi, 100);
  // 遮蔽字段取并集
  assert.deepEqual(decision.merged.solution.maskedFields, ['salary', 'ssn']);
  // 审计标签并集
  assert.deepEqual(decision.merged.solution.auditTags, ['read']);
  assert.equal(decision.merged.solution.format, 'json');
  // 来源证据
  assert.deepEqual(
    decision.merged.evidence.limits['maxRows']!.contributors.sort(),
    ['permit-analyst', 'permit-finance'],
  );
});

test('deny 不执行允许义务，被抑制义务显式保留不丢弃', () => {
  const noMfa = new AttributeStore({
    clearance: intAttr(3),
    department: setAttr(['finance']),
    mfaEnabled: boolAttr(false),
  });
  const decision = evaluate(policy, noMfa);
  assert.equal(decision.effect, 'deny');
  // 允许义务被抑制且显式记录来源与抑制者
  assert.equal(decision.suppressed.length, 6);
  assert.ok(decision.suppressed.every((s) => s.suppressedBy.includes('deny-no-mfa')));
  assert.deepEqual(
    [...new Set(decision.suppressed.map((s) => s.fromRule))].sort(),
    ['permit-analyst', 'permit-finance'],
  );
  // deny 自身的义务仍生效
  assert.equal(decision.merged.status, 'ok');
  if (decision.merged.status !== 'ok') return;
  assert.deepEqual(decision.merged.solution.auditTags, ['deny-no-mfa']);
});

test('撤回 deny 规则后恢复先前被压制的允许义务', () => {
  const noMfa = new AttributeStore({
    clearance: intAttr(3),
    department: setAttr(['finance']),
    mfaEnabled: boolAttr(false),
  });
  const denied = evaluate(policy, noMfa);
  assert.equal(denied.effect, 'deny');
  assert.ok(denied.suppressed.length > 0);

  const withdrawn = withdrawRule(policy, 'deny-no-mfa');
  const restored = evaluate(withdrawn, noMfa);
  assert.equal(restored.effect, 'permit');
  assert.equal(restored.suppressed.length, 0);
  assert.equal(restored.merged.status, 'ok');
  if (restored.merged.status !== 'ok') return;
  // 先前被压制的义务恢复生效
  assert.deepEqual(restored.merged.solution.maskedFields, ['salary', 'ssn']);
  assert.equal(restored.merged.solution.bounds['maxRows']!.hi, 100);

  // 原策略未被修改（不可变撤回）
  assert.equal(evaluate(policy, noMfa).effect, 'deny');

  // 恢复规则后再次抑制
  const reinstated = reinstateRule(withdrawn, 'deny-no-mfa');
  assert.equal(evaluate(reinstated, noMfa).effect, 'deny');
});

test('撤回不存在的规则明确失败', () => {
  assert.throws(() => withdrawRule(policy, 'no-such-rule'), /规则不存在/);
});

test('条件求值：整数比较、有限集合成员、布尔运算', () => {
  const rules: Rule[] = [
    {
      id: 'combo',
      effect: 'permit',
      condition: {
        op: 'and',
        conds: [
          { op: 'intCmp', attr: 'clearance', cmp: '>=', value: 3 },
          {
            op: 'or',
            conds: [
              { op: 'inSet', attr: 'department', value: 'legal' },
              { op: 'boolIs', attr: 'mfaEnabled', value: true },
            ],
          },
          { op: 'not', cond: { op: 'intCmp', attr: 'clearance', cmp: '<', value: 1 } },
        ],
      },
      obligations: [],
    },
  ];
  const decision = evaluate({ rules }, attrs);
  assert.deepEqual(decision.applicableRules, ['combo']);
});

test('读取未定义属性是明确失败而非静默为假', () => {
  const rules: Rule[] = [
    {
      id: 'bad',
      effect: 'permit',
      condition: { op: 'intCmp', attr: 'nonexistent', cmp: '==', value: 1 },
      obligations: [],
    },
  ];
  assert.throws(() => evaluate({ rules }, attrs), /属性未定义/);
});

test('属性存储只读：构造后不可修改', () => {
  const input = { clearance: intAttr(3) };
  const store = new AttributeStore(input);
  assert.equal(Object.isFrozen(store), true);
  // 修改构造入参不影响存储（已快照）
  (input as Record<string, unknown>)['clearance'] = intAttr(99);
  assert.equal(store.getInt('clearance'), 3);
  // 存储不提供任何修改方法
  assert.equal(typeof (store as Record<string, unknown>)['set'], 'undefined');
});
