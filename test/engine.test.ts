import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CommissionEngine } from '../src/engine.ts';

/** alice -> bob(5%) / alice -> carol(3%) / carol -> bob(10%) / bob -> dave(20%) */
function buildEngine(caps: Record<string, number> = {}): CommissionEngine {
  const engine = new CommissionEngine();
  for (const id of ['alice', 'bob', 'carol', 'dave']) {
    engine.registerAgent(id, caps[id] === undefined ? {} : { monthlyCap: caps[id] });
  }
  engine.addAttributionEdge('alice', 'bob', 500);
  engine.addAttributionEdge('alice', 'carol', 300);
  engine.addAttributionEdge('carol', 'bob', 1000);
  engine.addAttributionEdge('bob', 'dave', 2000);
  return engine;
}

test('多路径到达同一上级只计一次，取有效费率最高的链', () => {
  const engine = buildEngine();
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 10000, month: '2026-09' });

  const payouts = engine.getLedger().filter((e) => e.kind === 'PAYOUT');
  const byRecipient = new Map(payouts.map((e) => [e.recipient, e.amount]));
  // bob 直连 5%（500）优于经 carol 的 3%*10%=0.3%（30），只发一次
  assert.equal(byRecipient.get('bob'), 500);
  assert.equal(byRecipient.get('carol'), 300);
  // dave 经 bob: 5%*20% = 1% -> 100；经 carol->bob: 3%*20% = 0.6% 被去重
  assert.equal(byRecipient.get('dave'), 100);
  assert.equal(payouts.filter((e) => e.recipient === 'bob').length, 1);
  assert.equal(payouts.filter((e) => e.recipient === 'dave').length, 1);
});

test('explainTransaction 输出完整来源链并标注去重结果', () => {
  const engine = buildEngine();
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 10000, month: '2026-09' });

  const chains = engine.explainTransaction('tx1');
  assert.equal(chains.length, 5);
  const key = (c: { chain: { steps: { from: string; to: string }[] } }) =>
    c.chain.steps.map((s) => `${s.from}>${s.to}`).join('|');
  const selected = new Set(chains.filter((c) => c.selected).map(key));
  assert.deepEqual(selected, new Set(['alice>bob', 'alice>carol', 'alice>bob|bob>dave']));

  const viaCarol = chains.find((c) => key(c) === 'alice>carol|carol>bob')!;
  assert.equal(viaCarol.selected, false);
  assert.equal(viaCarol.dueAmount, 30);
  assert.equal(viaCarol.chain.effectiveRateBps, 30);
});

test('月封顶截断超出部分并进入待补发队列', () => {
  const engine = buildEngine({ bob: 400 });
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 10000, month: '2026-09' });
  engine.submitTransaction({ id: 'tx2', agentId: 'alice', amount: 10000, month: '2026-09' });

  const bobPaid = engine.getLedger()
    .filter((e) => e.recipient === 'bob' && e.kind === 'PAYOUT')
    .reduce((s, e) => s + e.amount, 0);
  assert.equal(bobPaid, 400);

  const pending = engine.getPending('2026-09').filter((p) => p.recipient === 'bob');
  assert.equal(pending.length, 2);
  assert.deepEqual(pending.map((p) => [p.txId, p.remaining]), [['tx1', 100], ['tx2', 500]]);

  const report = engine.monthlyReport('2026-09');
  assert.ok(report.balanced);
  const bob = report.recipients.find((r) => r.recipient === 'bob')!;
  assert.deepEqual([bob.due, bob.issued, bob.pending], [1000, 400, 600]);
});

test('撤单释放封顶后按原交易顺序补发被截部分', () => {
  const engine = buildEngine({ bob: 500 });
  // 每单 bob 应发 5% * 6000 = 300
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 6000, month: '2026-09' });
  engine.submitTransaction({ id: 'tx2', agentId: 'alice', amount: 6000, month: '2026-09' });
  engine.submitTransaction({ id: 'tx3', agentId: 'alice', amount: 6000, month: '2026-09' });
  // tx1 发 300；tx2 发 200 截 100；tx3 全截 300
  assert.deepEqual(
    engine.getPending('2026-09').filter((p) => p.recipient === 'bob').map((p) => [p.txId, p.remaining]),
    [['tx2', 100], ['tx3', 300]],
  );

  // 撤 tx1：冲回 300，释放额度；先补 tx2 的 100，再补 tx3 的 200（仍欠 100）
  const entries = engine.reverseTransaction('tx1');
  const reversals = entries.filter((e) => e.kind === 'REVERSAL');
  assert.equal(reversals.filter((e) => e.recipient === 'bob').length, 1);
  assert.equal(reversals.find((e) => e.recipient === 'bob')!.amount, -300);

  const reissues = entries.filter((e) => e.kind === 'REISSUE' && e.recipient === 'bob');
  assert.deepEqual(reissues.map((e) => [e.txId, e.amount]), [['tx2', 100], ['tx3', 200]]);

  assert.deepEqual(
    engine.getPending('2026-09').filter((p) => p.recipient === 'bob').map((p) => [p.txId, p.remaining]),
    [['tx3', 100]],
  );
  assert.ok(engine.verifyConservation());
});

test('撤单同时作废该单自身的待补发部分', () => {
  const engine = buildEngine({ bob: 400 });
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 10000, month: '2026-09' });
  engine.submitTransaction({ id: 'tx2', agentId: 'alice', amount: 10000, month: '2026-09' });
  // bob: tx1 发 400 截 100；tx2 全截 500

  engine.reverseTransaction('tx1');
  // tx1 的 100 待补发作废；释放的 400 全部补给 tx2
  const pending = engine.getPending('2026-09').filter((p) => p.recipient === 'bob');
  assert.deepEqual(pending.map((p) => [p.txId, p.remaining]), [['tx2', 100]]);

  const report = engine.monthlyReport('2026-09');
  const bob = report.recipients.find((r) => r.recipient === 'bob')!;
  assert.deepEqual([bob.due, bob.issued, bob.pending, bob.reversed], [500, 400, 100, 400]);
  assert.ok(report.balanced);
});

test('逐月守恒：跨月交易与撤单后 应发 = 实发净额 + 待补发', () => {
  const engine = buildEngine({ bob: 450, dave: 80 });
  engine.submitTransaction({ id: 't1', agentId: 'alice', amount: 10000, month: '2026-09' });
  engine.submitTransaction({ id: 't2', agentId: 'alice', amount: 7777, month: '2026-09' });
  engine.submitTransaction({ id: 't3', agentId: 'alice', amount: 12345, month: '2026-10' });
  engine.reverseTransaction('t1');
  engine.submitTransaction({ id: 't4', agentId: 'alice', amount: 999, month: '2026-10' });
  engine.reverseTransaction('t3');

  for (const month of ['2026-09', '2026-10']) {
    const report = engine.monthlyReport(month);
    assert.ok(report.balanced, `month ${month} unbalanced`);
    for (const row of report.recipients) {
      assert.equal(row.due, row.issued + row.pending, `${month}/${row.recipient}`);
    }
    assert.equal(report.totals.due, report.totals.issued + report.totals.pending);
  }
  assert.ok(engine.verifyConservation());
});

test('不同月份的封顶额度相互独立', () => {
  const engine = buildEngine({ bob: 400 });
  engine.submitTransaction({ id: 't1', agentId: 'alice', amount: 10000, month: '2026-09' });
  engine.submitTransaction({ id: 't2', agentId: 'alice', amount: 10000, month: '2026-10' });
  const paid = (m: string) => engine.getLedger()
    .filter((e) => e.recipient === 'bob' && e.month === m && e.kind === 'PAYOUT')
    .reduce((s, e) => s + e.amount, 0);
  assert.equal(paid('2026-09'), 400);
  assert.equal(paid('2026-10'), 400);
});

test('金额向下取整且守恒不被破坏', () => {
  const engine = buildEngine();
  engine.submitTransaction({ id: 't1', agentId: 'alice', amount: 999, month: '2026-09' });
  const byRecipient = new Map(
    engine.getLedger().filter((e) => e.kind === 'PAYOUT').map((e) => [e.recipient, e.amount]),
  );
  assert.equal(byRecipient.get('bob'), 49); // floor(999 * 5%)
  assert.equal(byRecipient.get('carol'), 29); // floor(999 * 3%)
  assert.equal(byRecipient.get('dave'), 9); // floor(999 * 1%)
  assert.ok(engine.verifyConservation());
});

test('非法输入：环、重复交易、未知代理人、重复撤单', () => {
  const engine = buildEngine();
  assert.throws(() => engine.addAttributionEdge('dave', 'alice', 100), /cycle/);
  assert.throws(() => engine.addAttributionEdge('alice', 'bob', 100), /duplicate edge/);
  engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 100, month: '2026-09' });
  assert.throws(() => engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 1, month: '2026-09' }), /duplicate transaction/);
  assert.throws(() => engine.submitTransaction({ id: 'tx2', agentId: 'nobody', amount: 1, month: '2026-09' }), /unknown agent/);
  assert.throws(() => engine.submitTransaction({ id: 'tx3', agentId: 'alice', amount: 1, month: '2026-9' }), /invalid month/);
  engine.reverseTransaction('tx1');
  assert.throws(() => engine.reverseTransaction('tx1'), /already reversed/);
  assert.throws(() => engine.reverseTransaction('nope'), /unknown transaction/);
});
