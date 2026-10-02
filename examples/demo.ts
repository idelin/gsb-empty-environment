import { RiskEngine } from '../src/index.ts';
import { ruleSetV1, ruleSetV2 } from '../rules/versions.ts';

const MINUTE = 60_000;
const t = (min: number): number => min * MINUTE;

const engine = new RiskEngine();

engine.publishRules(ruleSetV1);
console.log(`[t=0]   规则集 ${ruleSetV1.version} 生效: 10分钟内转账>3次 且 金额>10000 -> BLOCK`);

// t=90: 长事务会话开始, 锁定当时生效的 v1
engine.beginSession('S-long', t(90));
console.log(`[t=90]  长事务会话 S-long 开启, 锁定规则版本 ${engine.ruleVersionFor('S-long', t(90))}`);

// 长事务内的三笔小额历史转账 (落在最终判定的10分钟窗口内)
engine.submit({ id: 'tx-a1', sessionId: 'S-long', subjectId: 'user-A', action: 'transfer', amount: 100, timestamp: t(92) });
engine.submit({ id: 'tx-a2', sessionId: 'S-long', subjectId: 'user-A', action: 'transfer', amount: 100, timestamp: t(93) });
engine.submit({ id: 'tx-a3', sessionId: 'S-long', subjectId: 'user-A', action: 'transfer', amount: 100, timestamp: t(94) });

// 另一用户的三笔历史转账
engine.submit({ id: 'tx-b1', sessionId: 'S-b', subjectId: 'user-B', action: 'transfer', amount: 100, timestamp: t(95) });
engine.submit({ id: 'tx-b2', sessionId: 'S-b', subjectId: 'user-B', action: 'transfer', amount: 100, timestamp: t(96) });
engine.submit({ id: 'tx-b3', sessionId: 'S-b', subjectId: 'user-B', action: 'transfer', amount: 100, timestamp: t(97) });

// t=100: 规则库热切换为更严格的 v2
engine.publishRules(ruleSetV2);
console.log(`[t=100] 规则集 ${ruleSetV2.version} 热切换生效: 10分钟内转账>2次 且 金额>5000 -> BLOCK`);

// t=101: 长事务提交关键一笔 8000 元转账 -> 按旧规则 v1 裁定
const r1 = engine.submit({ id: 'tx-a4', sessionId: 'S-long', subjectId: 'user-A', action: 'transfer', amount: 8_000, timestamp: t(101) });
console.log(`[t=101] S-long 提交 8000 元转账 -> ${r1.decision} (规则版本 ${r1.ruleVersion})`);

// t=101: 全新请求, 同样的画像 -> 严格按新规则 v2 拦截
const r2 = engine.submit({ id: 'tx-b4', sessionId: 'S-b2', subjectId: 'user-B', action: 'transfer', amount: 8_000, timestamp: t(101) });
console.log(`[t=101] 新会话 S-b2 提交 8000 元转账 -> ${r2.decision} (规则版本 ${r2.ruleVersion})`);

console.log('\n=== 审计日志 ===');
for (const rec of engine.audit.all()) {
  const at = `t=${rec.timestamp / MINUTE}`;
  const rule = rec.matchedRuleId ?? '-';
  console.log(
    `#${rec.seq} ${at.padEnd(6)} tx=${rec.transactionId.padEnd(5)} session=${rec.sessionId.padEnd(7)} ` +
      `amount=${String(rec.amount).padEnd(5)} decision=${rec.decision.padEnd(6)} rule=${rule.padEnd(3)} ruleVersion=${rec.ruleVersion}`,
  );
}

const v1Count = engine.audit.byVersion('v1').length;
const v2Count = engine.audit.byVersion('v2').length;
console.log(`\n审计统计: v1 裁定 ${v1Count} 笔, v2 裁定 ${v2Count} 笔`);
