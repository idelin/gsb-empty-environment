import { RiskEngine } from './engine.ts';
import { ruleSetV1, ruleSetV2 } from './rules.ts';
import type { Transaction } from './types.ts';

/**
 * 验收场景重放：
 *  - t=90  长事务会话 S-LONG 开始，锁定规则 v1
 *  - t=100 规则库热切换为更严格的 v2
 *  - t=101 会话内交易仍按 v1 裁定（放行）；新请求按 v2 裁定（拦截）
 *  - 审计日志中两者标记不同的规则版本号
 */

const engine = new RiskEngine(ruleSetV1);
console.log(`[t=0]   引擎启动，生效规则版本: ${engine.activeVersion}`);

// t=90：长事务开始，锁定 v1
engine.beginSession('S-LONG');
console.log(`[t=90]  长事务 S-LONG 开始，锁定规则版本: ${engine.pinnedVersion('S-LONG')}`);

const tx = (
  id: string,
  accountId: string,
  amount: number,
  timestamp: number,
  sessionId?: string,
): Transaction => ({
  id,
  accountId,
  type: 'transfer',
  amount,
  currency: 'CNY',
  timestamp,
  sessionId,
});

// 长事务内的三笔转账（t=91/95/99）：10 分钟内 3 笔、累计 8500
for (const t of [
  tx('TX-1', 'acct-A', 3000, 91, 'S-LONG'),
  tx('TX-2', 'acct-A', 3000, 95, 'S-LONG'),
  tx('TX-3', 'acct-A', 2500, 99, 'S-LONG'),
]) {
  const d = engine.evaluate(t);
  console.log(`[t=${t.timestamp}]  ${t.id} (会话S-LONG) -> ${d.action} [规则版本 ${d.ruleVersion}]`);
}

// t=100：规则库热切换为 v2（更严格）
engine.updateRules(ruleSetV2);
console.log(`[t=100] 规则库热切换 -> 新请求生效版本: ${engine.activeVersion}；S-LONG 仍锁定: ${engine.pinnedVersion('S-LONG')}`);

// t=101：长事务第 4 笔（窗口内 4 笔、累计 9500）
//   v1 阈值：>3 次且 >10000 -> 金额未超，放行；若按 v2 早已拦截
const d1 = engine.evaluate(tx('TX-4', 'acct-A', 1000, 101, 'S-LONG'));
console.log(`[t=101] TX-4 (会话S-LONG, 旧规则) -> ${d1.action} [规则版本 ${d1.ruleVersion}]  ${d1.reason}`);

// t=101 起：另一账户的新请求，按 v2 严格拦截（3 笔累计 6000 即触发）
for (const t of [
  tx('TX-5', 'acct-B', 2000, 101),
  tx('TX-6', 'acct-B', 2000, 102),
  tx('TX-7', 'acct-B', 2000, 103),
]) {
  const d = engine.evaluate(t);
  console.log(`[t=${t.timestamp}] ${t.id} (新请求, 新规则) -> ${d.action} [规则版本 ${d.ruleVersion}]  ${d.reason}`);
}

// 长事务结束，同账户后续请求回到当前生效版本 v2
engine.endSession('S-LONG');
const d2 = engine.evaluate(tx('TX-8', 'acct-A', 600, 110));
console.log(`[t=110] TX-8 (会话已结束) -> ${d2.action} [规则版本 ${d2.ruleVersion}]  ${d2.reason}`);

console.log('\n===== 审计日志 =====');
for (const e of engine.audit.all()) {
  console.log(
    `#${e.seq} t=${e.timestamp} ${e.txId} session=${e.sessionId ?? '-'} ` +
      `version=${e.ruleVersion} action=${e.action} rules=[${e.matchedRuleIds.join(',')}]`,
  );
}
