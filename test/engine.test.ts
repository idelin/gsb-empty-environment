import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RiskEngine, WindowStore, RuleRegistry } from '../src/index.ts';
import { ruleSetV1, ruleSetV2 } from '../rules/versions.ts';
import type { Transaction } from '../src/index.ts';

const MINUTE = 60_000;
const t = (min: number): number => min * MINUTE;

function tx(partial: Partial<Transaction> & Pick<Transaction, 'id' | 'timestamp'>): Transaction {
  return {
    sessionId: 'S',
    subjectId: 'user',
    action: 'transfer',
    amount: 0,
    ...partial,
  };
}

test('滑动窗口: 统计窗口内频次与金额, 窗口外事件不计入', () => {
  const store = new WindowStore();
  store.record({ subjectId: 'u', action: 'transfer', amount: 10, timestamp: t(1) });
  store.record({ subjectId: 'u', action: 'transfer', amount: 20, timestamp: t(5) });
  store.record({ subjectId: 'u', action: 'transfer', amount: 40, timestamp: t(9) });
  store.record({ subjectId: 'u', action: 'login', amount: 0, timestamp: t(9) });
  store.record({ subjectId: 'other', action: 'transfer', amount: 99, timestamp: t(9) });

  const stats = store.query('u', 'transfer', t(10), 10 * MINUTE);
  assert.deepEqual(stats, { count: 3, sum: 70 });

  // 窗口左边界为开区间: t=1 的事件在 (t=2, t=12] 窗口外
  const later = store.query('u', 'transfer', t(12), 10 * MINUTE);
  assert.deepEqual(later, { count: 2, sum: 60 });
});

test('规则注册表: 按时间点解析生效版本, 拒绝重复发布', () => {
  const registry = new RuleRegistry();
  registry.publish(ruleSetV1);
  registry.publish(ruleSetV2);
  assert.equal(registry.activeAt(t(0)).version, 'v1');
  assert.equal(registry.activeAt(t(99)).version, 'v1');
  assert.equal(registry.activeAt(t(100)).version, 'v2');
  assert.equal(registry.current().version, 'v2');
  assert.throws(() => registry.publish(ruleSetV1), /already published/);
});

test('组合条件: 单笔特征与窗口频次同时满足才命中', () => {
  const engine = new RiskEngine();
  engine.publishRules(ruleSetV1);

  // 金额超限但窗口频次不足 -> ALLOW
  for (let i = 0; i < 2; i += 1) {
    engine.submit(tx({ id: `p${i}`, timestamp: t(1 + i), amount: 100 }));
  }
  const r1 = engine.submit(tx({ id: 'big-few', timestamp: t(3), amount: 20_000 }));
  assert.equal(r1.decision, 'ALLOW');

  // 频次超限但金额不足 -> ALLOW
  for (let i = 0; i < 4; i += 1) {
    engine.submit(tx({ id: `q${i}`, timestamp: t(4 + i), amount: 10 }));
  }
  const r2 = engine.submit(tx({ id: 'small-many', timestamp: t(9), amount: 10 }));
  assert.equal(r2.decision, 'ALLOW');

  // 两者同时超限 -> BLOCK (当前 t=9 的窗口内已有 6 笔历史转账)
  const r3 = engine.submit(tx({ id: 'big-many', timestamp: t(9), amount: 20_000 }));
  assert.equal(r3.decision, 'BLOCK');
  assert.equal(r3.matchedRuleId, 'R1');
});

test('验收边界: t=90 长事务按旧规则放行, t=101 新请求按新规则拦截, 审计标记不同版本', () => {
  const engine = new RiskEngine();
  engine.publishRules(ruleSetV1);

  // t=90: 长事务开启, 锁定 v1
  engine.beginSession('S-long', t(90));
  engine.submit(tx({ id: 'a1', sessionId: 'S-long', subjectId: 'user-A', timestamp: t(92), amount: 100 }));
  engine.submit(tx({ id: 'a2', sessionId: 'S-long', subjectId: 'user-A', timestamp: t(93), amount: 100 }));
  engine.submit(tx({ id: 'a3', sessionId: 'S-long', subjectId: 'user-A', timestamp: t(94), amount: 100 }));

  engine.submit(tx({ id: 'b1', sessionId: 'S-b', subjectId: 'user-B', timestamp: t(95), amount: 100 }));
  engine.submit(tx({ id: 'b2', sessionId: 'S-b', subjectId: 'user-B', timestamp: t(96), amount: 100 }));
  engine.submit(tx({ id: 'b3', sessionId: 'S-b', subjectId: 'user-B', timestamp: t(97), amount: 100 }));

  // t=100: 热切换为更严格的 v2
  engine.publishRules(ruleSetV2);

  // t=101: 长事务按旧规则 v1 裁定 -> 窗口内3次未超 v1 的 ">3", 8000 未超 10000 -> ALLOW
  const oldResult = engine.submit(
    tx({ id: 'a4', sessionId: 'S-long', subjectId: 'user-A', timestamp: t(101), amount: 8_000 }),
  );
  assert.equal(oldResult.decision, 'ALLOW');
  assert.equal(oldResult.ruleVersion, 'v1');

  // t=101: 新请求严格按新规则 v2 -> 窗口内3次 > 2 且 8000 > 5000 -> BLOCK
  const newResult = engine.submit(
    tx({ id: 'b4', sessionId: 'S-b2', subjectId: 'user-B', timestamp: t(101), amount: 8_000 }),
  );
  assert.equal(newResult.decision, 'BLOCK');
  assert.equal(newResult.ruleVersion, 'v2');
  assert.equal(newResult.matchedRuleId, 'R1');

  // 审计日志: 同一时刻两笔交易标记了不同的规则版本号
  const auditA4 = engine.audit.all().find((r) => r.transactionId === 'a4');
  const auditB4 = engine.audit.all().find((r) => r.transactionId === 'b4');
  assert.equal(auditA4?.ruleVersion, 'v1');
  assert.equal(auditB4?.ruleVersion, 'v2');
  assert.equal(auditA4?.timestamp, auditB4?.timestamp);
  assert.ok(engine.audit.byVersion('v1').length > 0);
  assert.ok(engine.audit.byVersion('v2').length > 0);
});

test('热切换后新会话自动使用当前生效版本', () => {
  const engine = new RiskEngine();
  engine.publishRules(ruleSetV1);
  engine.publishRules(ruleSetV2);

  // 未显式 beginSession 的会话按首笔交易时间解析版本
  const before = engine.submit(tx({ id: 'x1', sessionId: 'S-x', timestamp: t(50), amount: 10 }));
  assert.equal(before.ruleVersion, 'v1');

  const after = engine.submit(tx({ id: 'y1', sessionId: 'S-y', timestamp: t(101), amount: 10 }));
  assert.equal(after.ruleVersion, 'v2');
});

test('优先级: 高优先级规则先命中, 无命中默认放行', () => {
  const engine = new RiskEngine();
  engine.publishRules({
    version: 'v-priority',
    effectiveFrom: 0,
    rules: [
      { id: 'low', name: '低优先级复核', decision: 'REVIEW', priority: 1, tx: { amountGt: 100 } },
      { id: 'high', name: '高优先级拦截', decision: 'BLOCK', priority: 10, tx: { amountGt: 100 } },
    ],
  });
  const hit = engine.submit(tx({ id: 'p1', timestamp: t(1), amount: 200 }));
  assert.equal(hit.decision, 'BLOCK');
  assert.equal(hit.matchedRuleId, 'high');

  const miss = engine.submit(tx({ id: 'p2', timestamp: t(1), amount: 50 }));
  assert.equal(miss.decision, 'ALLOW');
  assert.equal(miss.matchedRuleId, null);
});
