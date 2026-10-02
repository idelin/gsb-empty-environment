import { test } from 'node:test';
import assert from 'node:assert/strict';
import { RiskEngine } from '../src/engine.ts';
import { ruleSetV1, ruleSetV2 } from '../src/rules.ts';
import type { Transaction } from '../src/types.ts';

let seq = 0;
function tx(partial: Partial<Transaction> & { timestamp: number }): Transaction {
  seq += 1;
  return {
    id: `T${seq}`,
    accountId: 'acct-1',
    type: 'transfer',
    amount: 100,
    currency: 'CNY',
    ...partial,
  };
}

test('滑动窗口：10分钟内转账超3次且累计超10000 触发拦截', () => {
  const engine = new RiskEngine(ruleSetV1);
  // 4 笔转账，窗口内累计 12000 -> 第 4 笔触发
  assert.equal(engine.evaluate(tx({ timestamp: 0, amount: 3000 })).action, 'allow');
  assert.equal(engine.evaluate(tx({ timestamp: 60, amount: 3000 })).action, 'allow');
  assert.equal(engine.evaluate(tx({ timestamp: 120, amount: 3000 })).action, 'allow');
  const d = engine.evaluate(tx({ timestamp: 180, amount: 3000 }));
  assert.equal(d.action, 'block');
  assert.deepEqual(d.matchedRuleIds, ['R-TRANSFER-FREQ']);
});

test('滑动窗口：频次达标但累计金额未超阈值 -> 放行', () => {
  const engine = new RiskEngine(ruleSetV1);
  for (let i = 0; i < 4; i++) {
    // 4 笔各 1000，累计 4000 <= 10000
    assert.equal(engine.evaluate(tx({ timestamp: i * 10, amount: 1000 })).action, 'allow');
  }
});

test('滑动窗口：窗口外的历史交易不计入聚合', () => {
  const engine = new RiskEngine(ruleSetV1);
  // t=0 的三笔大额在 t=601 时已滑出 600 秒窗口
  engine.evaluate(tx({ timestamp: 0, amount: 9000 }));
  engine.evaluate(tx({ timestamp: 1, amount: 9000 }));
  engine.evaluate(tx({ timestamp: 2, amount: 9000 }));
  const d = engine.evaluate(tx({ timestamp: 601, amount: 9000 }));
  assert.equal(d.action, 'allow');
});

test('滑动窗口：聚合按账户隔离，且只统计规则指定的交易类型', () => {
  const engine = new RiskEngine(ruleSetV1);
  // acct-2 的转账不影响 acct-1；payment 类型不计入 transfer 频次
  engine.evaluate(tx({ accountId: 'acct-2', timestamp: 0, amount: 9000 }));
  engine.evaluate(tx({ timestamp: 1, amount: 9000, type: 'payment' }));
  engine.evaluate(tx({ timestamp: 2, amount: 9000 }));
  const d = engine.evaluate(tx({ timestamp: 3, amount: 9000 }));
  assert.equal(d.action, 'allow'); // 窗口内仅 2 笔 transfer + 当前 = 3，未超 3
});

test('热切换：updateRules 后新请求立即按新版本裁定', () => {
  const engine = new RiskEngine(ruleSetV1);
  engine.evaluate(tx({ timestamp: 0, amount: 3000 }));
  engine.evaluate(tx({ timestamp: 10, amount: 3000 }));
  engine.updateRules(ruleSetV2);
  assert.equal(engine.activeVersion, 'v2');
  // v2 阈值：>2 次且 >5000 -> 第 3 笔即拦截（v1 下不会拦）
  const d = engine.evaluate(tx({ timestamp: 20, amount: 3000 }));
  assert.equal(d.action, 'block');
  assert.equal(d.ruleVersion, 'v2');
});

test('会话锁定：长事务在热切换后仍按旧版本平稳过渡', () => {
  const engine = new RiskEngine(ruleSetV1);
  engine.beginSession('S1');
  engine.evaluate(tx({ timestamp: 0, amount: 3000, sessionId: 'S1' }));
  engine.evaluate(tx({ timestamp: 10, amount: 3000, sessionId: 'S1' }));
  engine.updateRules(ruleSetV2);
  // 同样的第 3 笔：会话内按 v1 放行，无会话按 v2 拦截
  const inSession = engine.evaluate(tx({ timestamp: 20, amount: 3000, sessionId: 'S1' }));
  assert.equal(inSession.action, 'allow');
  assert.equal(inSession.ruleVersion, 'v1');
  const noSession = engine.evaluate(tx({ timestamp: 21, amount: 3000 }));
  assert.equal(noSession.action, 'block');
  assert.equal(noSession.ruleVersion, 'v2');
});

test('会话结束后回到当前生效版本', () => {
  const engine = new RiskEngine(ruleSetV1);
  engine.beginSession('S1');
  engine.updateRules(ruleSetV2);
  engine.endSession('S1');
  const d = engine.evaluate(tx({ timestamp: 0, amount: 30000, sessionId: 'S1' }));
  // v1 对 30000 仅 review（>50000 才触发），v2 直接 block（>20000）
  assert.equal(d.action, 'block');
  assert.equal(d.ruleVersion, 'v2');
});

test('验收边界：t=100 规则更新，t=90 长事务按 v1，t=101 新请求按 v2，审计版本号不同', () => {
  const engine = new RiskEngine(ruleSetV1);

  // t=90：长事务开始，锁定 v1
  engine.beginSession('S-LONG');
  engine.evaluate(tx({ id: 'L1', accountId: 'acct-A', timestamp: 91, amount: 3000, sessionId: 'S-LONG' }));
  engine.evaluate(tx({ id: 'L2', accountId: 'acct-A', timestamp: 95, amount: 3000, sessionId: 'S-LONG' }));
  engine.evaluate(tx({ id: 'L3', accountId: 'acct-A', timestamp: 99, amount: 2500, sessionId: 'S-LONG' }));

  // t=100：规则库更新为更严格的 v2
  engine.updateRules(ruleSetV2);

  // t=101：长事务第 4 笔，窗口内 4 笔累计 9500
  //   v1（>3 且 >10000）-> 放行；v2（>2 且 >5000）-> 本应拦截
  const longTx = engine.evaluate(tx({ id: 'L4', accountId: 'acct-A', timestamp: 101, amount: 1000, sessionId: 'S-LONG' }));
  assert.equal(longTx.action, 'allow');
  assert.equal(longTx.ruleVersion, 'v1');

  // t=101：新请求严格按 v2 拦截（3 笔累计 6000 即触发）
  engine.evaluate(tx({ id: 'N1', accountId: 'acct-B', timestamp: 101, amount: 2000 }));
  engine.evaluate(tx({ id: 'N2', accountId: 'acct-B', timestamp: 101, amount: 2000 }));
  const newReq = engine.evaluate(tx({ id: 'N3', accountId: 'acct-B', timestamp: 101, amount: 2000 }));
  assert.equal(newReq.action, 'block');
  assert.equal(newReq.ruleVersion, 'v2');

  // 审计日志：两者标记不同规则版本号
  const auditLong = engine.audit.findByTx('L4');
  const auditNew = engine.audit.findByTx('N3');
  assert.ok(auditLong && auditNew);
  assert.equal(auditLong.ruleVersion, 'v1');
  assert.equal(auditNew.ruleVersion, 'v2');
  assert.notEqual(auditLong.ruleVersion, auditNew.ruleVersion);
});

test('审计日志：每次裁决均记录版本号、动作与命中规则', () => {
  const engine = new RiskEngine(ruleSetV1);
  engine.evaluate(tx({ id: 'A1', timestamp: 0, amount: 60000 }));
  const entry = engine.audit.findByTx('A1');
  assert.ok(entry);
  assert.equal(entry.seq, 1);
  assert.equal(entry.ruleVersion, 'v1');
  assert.equal(entry.action, 'review'); // 单笔超 50000 -> review
  assert.deepEqual(entry.matchedRuleIds, ['R-LARGE-SINGLE']);
});
