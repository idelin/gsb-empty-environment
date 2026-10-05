import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CommissionEngine } from '../src/index.ts';
import type { DistributionRecord } from '../src/index.ts';

const cents = (yuan: number): number => Math.round(yuan * 100);

function invariantHolds(records: DistributionRecord[]): void {
  for (const r of records) {
    assert.equal(
      r.grossCents,
      r.paidCents + r.reversedCents + r.pendingCents + r.voidedCents,
      `record invariant broken for ${r.txId}->${r.recipient}`,
    );
  }
}

test('线性链分账并输出完整来源链', () => {
  const e = new CommissionEngine();
  e.addAgent('A');
  e.addAgent('B');
  e.addAgent('C');
  e.setUpline('A', 'B', 0.1);
  e.setUpline('B', 'C', 0.5);

  const recs = e.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
  assert.equal(recs.length, 2);

  const b = recs.find((r) => r.recipient === 'B')!;
  const c = recs.find((r) => r.recipient === 'C')!;
  assert.equal(b.grossCents, cents(100));
  assert.deepEqual(b.effectiveChain.path, ['A', 'B']);
  assert.equal(c.grossCents, cents(50));
  assert.deepEqual(c.effectiveChain.path, ['A', 'B', 'C']);
  assert.equal(c.effectiveChain.rate, 0.05);
  invariantHolds(recs);
});

test('菱形图同一上级多路径只计一次，保留全部来源链', () => {
  const e = new CommissionEngine();
  e.setUpline('A', 'B', 0.2);
  e.setUpline('A', 'C', 0.3);
  e.setUpline('B', 'D', 0.5);
  e.setUpline('C', 'D', 0.4);

  const recs = e.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
  const d = recs.filter((r) => r.recipient === 'D');
  assert.equal(d.length, 1, '同一上级每笔交易只计一次');
  assert.equal(d[0].chains.length, 2, '完整来源链包含两条路径');
  // max(0.2*0.5, 0.3*0.4) = max(0.10, 0.12) = 0.12
  assert.equal(d[0].grossCents, cents(120));
  assert.deepEqual(d[0].effectiveChain.path, ['A', 'C', 'D']);
});

test('成环与非法边被拒绝', () => {
  const e = new CommissionEngine();
  e.setUpline('A', 'B', 0.5);
  e.setUpline('B', 'C', 0.5);
  assert.throws(() => e.setUpline('C', 'A', 0.5), /cycle/);
  assert.throws(() => e.setUpline('A', 'A', 0.5), /self-loop/);
  assert.throws(() => e.setUpline('X', 'Y', 0), /rate/);
  assert.throws(() => e.setUpline('X', 'Y', 1.5), /rate/);
});

test('月封顶截断超出部分进入待补发', () => {
  const e = new CommissionEngine();
  e.addAgent('B', { monthlyCap: 150 });
  e.setUpline('A', 'B', 0.1);

  e.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
  const recs = e.distribute({ txId: 'T2', source: 'A', amount: 1000, month: '2026-10' });

  const r2 = recs.find((r) => r.recipient === 'B')!;
  assert.equal(r2.grossCents, cents(100));
  assert.equal(r2.paidCents, cents(50));
  assert.equal(r2.pendingCents, cents(50));
  invariantHolds(recs);
});

test('撤单释放封顶后按原交易顺序补发被截部分', () => {
  const e = new CommissionEngine();
  e.addAgent('B', { monthlyCap: 100 });
  e.setUpline('A', 'B', 1);

  e.distribute({ txId: 'T1', source: 'A', amount: 100, month: '2026-10' }); // 占满封顶
  e.distribute({ txId: 'T2', source: 'A', amount: 80, month: '2026-10' }); // 全截
  e.distribute({ txId: 'T3', source: 'A', amount: 50, month: '2026-10' }); // 全截

  const corrections = e.cancel('T1', '退款');
  const kinds = corrections.map((c) => c.kind);
  assert.deepEqual(kinds, ['reverse', 'backpay', 'backpay']);

  const st = e.statement('B', '2026-10');
  const t2 = st.records.find((r) => r.txId === 'T2')!;
  const t3 = st.records.find((r) => r.txId === 'T3')!;
  assert.equal(t2.paidCents, cents(80), '先补发较早的 T2');
  assert.equal(t2.pendingCents, 0);
  assert.equal(t3.paidCents, cents(20), '剩余额度 20 部分补发 T3');
  assert.equal(t3.pendingCents, cents(30));
  assert.equal(st.netPaidCents, cents(100), '补发后仍不超封顶');
  invariantHolds(st.records);
});

test('撤单作废自身被截部分，释放的额度补发他人', () => {
  const e = new CommissionEngine();
  e.addAgent('B', { monthlyCap: 100 });
  e.setUpline('A', 'B', 1);

  e.distribute({ txId: 'T1', source: 'A', amount: 150, month: '2026-10' }); // 付100截50
  e.distribute({ txId: 'T2', source: 'A', amount: 40, month: '2026-10' }); // 全截

  e.cancel('T1');
  const st = e.statement('B', '2026-10');
  const t1 = st.records.find((r) => r.txId === 'T1')!;
  const t2 = st.records.find((r) => r.txId === 'T2')!;
  assert.equal(t1.reversedCents, cents(100));
  assert.equal(t1.voidedCents, cents(50));
  assert.equal(t1.status, 'cancelled');
  assert.equal(t2.paidCents, cents(40), 'T1 释放额度后 T2 全额补发');
  assert.equal(st.netPaidCents, cents(40));
  invariantHolds(st.records);
});

test('跨月混合操作后逐月金额守恒且无超封顶', () => {
  const e = new CommissionEngine();
  e.addAgent('B', { monthlyCap: 120 });
  e.addAgent('C', { monthlyCap: 60 });
  e.setUpline('A', 'B', 0.2);
  e.setUpline('B', 'C', 0.5);

  e.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
  e.distribute({ txId: 'T2', source: 'A', amount: 500, month: '2026-10' });
  e.distribute({ txId: 'T3', source: 'A', amount: 800, month: '2026-11' });
  e.cancel('T1');
  e.distribute({ txId: 'T4', source: 'A', amount: 300, month: '2026-11' });
  e.cancel('T3');

  const report = e.reconcile();
  assert.deepEqual(report.map((r) => r.month), ['2026-10', '2026-11']);
  for (const row of report) {
    assert.ok(row.conserved, `${row.month} 金额不守恒`);
    assert.deepEqual(row.capViolations, []);
  }
  invariantHolds([...e.allRecords()]);
});

test('非法输入与重复操作抛错', () => {
  const e = new CommissionEngine();
  e.addAgent('A');
  e.setUpline('A', 'B', 0.1);
  e.distribute({ txId: 'T1', source: 'A', amount: 100, month: '2026-10' });

  assert.throws(() => e.distribute({ txId: 'T1', source: 'A', amount: 1, month: '2026-10' }), /duplicate/);
  assert.throws(() => e.distribute({ txId: 'T2', source: 'ZZ', amount: 1, month: '2026-10' }), /unknown agent/);
  assert.throws(() => e.distribute({ txId: 'T3', source: 'A', amount: -1, month: '2026-10' }), /positive/);
  assert.throws(() => e.distribute({ txId: 'T4', source: 'A', amount: 1, month: '2026-13' }), /bad month/);
  assert.throws(() => e.cancel('NOPE'), /unknown txId/);
  e.cancel('T1');
  assert.throws(() => e.cancel('T1'), /already cancelled/);
});
