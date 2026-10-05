import { test } from 'node:test';
import assert from 'node:assert/strict';
import { frac } from '../src/fraction.ts';
import { Ledger, nextDay, quarterRange } from '../src/ledger.ts';

const Q1_FROM = '2025-01-01';
const Q1_TO = '2025-04-01';

function eachDay(from: string, to: string): string[] {
  const days: string[] = [];
  for (let d = from; d < to; d = nextDay(d)) days.push(d);
  return days;
}

function addDailyScores(
  ledger: Ledger,
  from: string,
  to: string,
  startSeq: number,
  values: Record<string, (date: string) => ReturnType<typeof frac>>,
  skip?: (date: string, metric: string) => boolean,
): number {
  let seq = startSeq;
  for (const date of eachDay(from, to)) {
    for (const metric of Object.keys(values)) {
      if (skip && skip(date, metric)) continue;
      ledger.addScore({ seq, date, metric, value: values[metric](date) });
      seq += 1;
    }
  }
  return seq;
}

test('quarterRange 边界', () => {
  assert.deepEqual(quarterRange('2025Q1'), { from: '2025-01-01', to: '2025-04-01' });
  assert.deepEqual(quarterRange('2025Q4'), { from: '2025-10-01', to: '2026-01-01' });
});

test('单规则整季度：加权分的精确算术平均', () => {
  const ledger = new Ledger();
  ledger.addRevision({
    seq: 1,
    ruleId: 'base',
    validFrom: Q1_FROM,
    validTo: Q1_TO,
    weights: { a: frac(1, 3), b: frac(2, 3) },
  });
  addDailyScores(ledger, Q1_FROM, Q1_TO, 10, { a: () => frac(1, 7), b: () => frac(1) });

  const result = ledger.queryQuarterLabel('2025Q1');
  assert.equal(result.totalDays, 90);
  assert.equal(result.scoredDays, 90);
  // 每日 1/3*1/7 + 2/3*1 = 5/7，90 天平均仍是 5/7
  assert.equal(result.total!.toString(), '5/7');
  assert.deepEqual(
    result.segments,
    [{ from: Q1_FROM, to: Q1_TO, ruleId: 'base', ruleSeq: 1 }],
  );
  assert.deepEqual(result.missing, []);
});

test('跨月修订：输出规则版本分段', () => {
  const ledger = new Ledger();
  ledger.addRevision({ seq: 1, ruleId: 'base', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1) } });
  ledger.addRevision({
    seq: 2,
    ruleId: 'patch',
    validFrom: '2025-02-15',
    validTo: '2025-03-10',
    weights: { a: frac(1, 2), b: frac(1, 2) },
  });
  addDailyScores(ledger, Q1_FROM, Q1_TO, 10, { a: () => frac(1), b: () => frac(1, 2) });

  const result = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.deepEqual(result.segments, [
    { from: '2025-01-01', to: '2025-02-15', ruleId: 'base', ruleSeq: 1 },
    { from: '2025-02-15', to: '2025-03-10', ruleId: 'patch', ruleSeq: 2 },
    { from: '2025-03-10', to: '2025-04-01', ruleId: 'base', ruleSeq: 1 },
  ]);
  // 67 天 * 1 + 23 天 * 3/4 = 337/4，平均 337/360
  assert.equal(result.total!.toString(), '337/360');
});

function buildRetroactiveLedger(): Ledger {
  const ledger = new Ledger();
  ledger.addRevision({ seq: 1, ruleId: 'base', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1) } });
  addDailyScores(ledger, Q1_FROM, Q1_TO, 10, { a: () => frac(1, 2), b: () => frac(1) });
  // 季中才录入（seq 2000），但业务有效期追溯到季初
  ledger.addRevision({
    seq: 2000,
    ruleId: 'retro',
    validFrom: Q1_FROM,
    validTo: Q1_TO,
    weights: { a: frac(1, 4), b: frac(3, 4) },
  });
  return ledger;
}

test('双时点：中途录入却追溯到季初的权重', () => {
  const ledger = buildRetroactiveLedger();

  const then = ledger.queryQuarter(Q1_FROM, Q1_TO, 1500);
  assert.equal(then.total!.toString(), '1/2');
  assert.deepEqual(
    then.segments,
    [{ from: Q1_FROM, to: Q1_TO, ruleId: 'base', ruleSeq: 1 }],
  );

  const now = ledger.queryQuarter(Q1_FROM, Q1_TO);
  // 1/4*1/2 + 3/4*1 = 7/8，整季都按新规则重算
  assert.equal(now.total!.toString(), '7/8');
  assert.deepEqual(
    now.segments,
    [{ from: Q1_FROM, to: Q1_TO, ruleId: 'retro', ruleSeq: 2000 }],
  );
});

test('撤销最新规则：仅撤销序号之后生效，随后回退前一版', () => {
  const ledger = buildRetroactiveLedger();
  ledger.addRevocation(2001, 2000);

  // 撤销序号之前仍看到 retro
  const beforeRevoke = ledger.queryQuarter(Q1_FROM, Q1_TO, 2000);
  assert.equal(beforeRevoke.total!.toString(), '7/8');

  // 当前视角回退到 base
  const now = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.equal(now.total!.toString(), '1/2');
  assert.deepEqual(
    now.segments,
    [{ from: Q1_FROM, to: Q1_TO, ruleId: 'base', ruleSeq: 1 }],
  );
});

test('同序号冲突与输入校验', () => {
  const ledger = new Ledger();
  ledger.addRevision({ seq: 3, ruleId: 'r', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1) } });
  assert.throws(
    () => ledger.addRevision({ seq: 3, ruleId: 'r2', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1) } }),
    /duplicate entry seq 3/,
  );
  assert.throws(() => ledger.addScore({ seq: 3, date: Q1_FROM, metric: 'a', value: frac(1) }), /duplicate entry seq 3/);
  assert.throws(() => ledger.addRevocation(3, 1), /duplicate entry seq 3/);
  assert.throws(() => ledger.addRevocation(4, 999), /unknown revision seq 999/);
  assert.throws(
    () => ledger.addRevision({ seq: 5, ruleId: 'bad', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1, 2) } }),
    /weights must sum to 1/,
  );
  assert.throws(
    () =>
      ledger.addRevision({
        seq: 6,
        ruleId: 'neg',
        validFrom: Q1_FROM,
        validTo: Q1_TO,
        weights: { a: frac(3, 2), b: frac(-1, 2) },
      }),
    /negative weight/,
  );
  assert.throws(
    () => ledger.addRevision({ seq: 7, ruleId: 'range', validFrom: Q1_TO, validTo: Q1_FROM, weights: { a: frac(1) } }),
    /validFrom must be before validTo/,
  );
});

test('缺分日期返回缺失项，而不是当 0', () => {
  const ledger = new Ledger();
  ledger.addRevision({
    seq: 1,
    ruleId: 'base',
    validFrom: Q1_FROM,
    validTo: Q1_TO,
    weights: { a: frac(1, 2), b: frac(1, 2) },
  });
  addDailyScores(
    ledger,
    Q1_FROM,
    Q1_TO,
    10,
    { a: () => frac(1, 3), b: () => frac(5, 6) },
    (date, metric) => (date === '2025-02-10' && metric === 'b') || date === '2025-03-05',
  );

  const result = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.equal(result.totalDays, 90);
  assert.equal(result.scoredDays, 88);
  assert.deepEqual(result.missing, [
    { date: '2025-02-10', metrics: ['b'] },
    { date: '2025-03-05', metrics: ['a', 'b'] },
  ]);
  // 完整日每日 1/2*1/3 + 1/2*5/6 = 7/12；88 个完整日平均仍是 7/12
  assert.equal(result.total!.toString(), '7/12');
  // 若缺分日被当 0 计入，结果会是 77/135 而非 7/12
  assert.notEqual(result.total!.toString(), '77/135');
});

test('批量补录：缓存只失效受影响区间', () => {
  const ledger = new Ledger();
  ledger.addRevision({ seq: 1, ruleId: 'base', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1) } });
  addDailyScores(ledger, Q1_FROM, Q1_TO, 10, { a: () => frac(1) });

  ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.equal(ledger.stats.dayComputes, 90);
  assert.equal(ledger.cacheSize(), 90);

  ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.equal(ledger.stats.dayComputes, 90, '第二次查询全部命中缓存');

  ledger.ingestBatch([
    { kind: 'revision', seq: 200, ruleId: 'patch', validFrom: '2025-02-01', validTo: '2025-02-11', weights: { a: frac(1) } },
    { kind: 'score', seq: 201, date: '2025-03-05', metric: 'a', value: frac(1, 2) },
  ]);
  assert.equal(ledger.cacheSize(), 90 - 11, '只失效 10 天修订区间 + 1 天补录');

  const result = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.equal(ledger.stats.dayComputes, 90 + 11, '只重算受影响的 11 天');
  const mar5 = result.days.find((d) => d.date === '2025-03-05');
  assert.equal(mar5!.status, 'ok');
  assert.equal((mar5 as { value: ReturnType<typeof frac> }).value.toString(), '1/2');
  assert.deepEqual(result.segments, [
    { from: '2025-01-01', to: '2025-02-01', ruleId: 'base', ruleSeq: 1 },
    { from: '2025-02-01', to: '2025-02-11', ruleId: 'patch', ruleSeq: 200 },
    { from: '2025-02-11', to: '2025-04-01', ruleId: 'base', ruleSeq: 1 },
  ]);
});

test('增量结果与独立逐日扫描一致（追溯、撤销、补录、缺分混合）', () => {
  const ledger = new Ledger();
  ledger.addRevision({
    seq: 1,
    ruleId: 'base',
    validFrom: Q1_FROM,
    validTo: Q1_TO,
    weights: { a: frac(1, 2), b: frac(1, 2) },
  });
  addDailyScores(
    ledger,
    Q1_FROM,
    Q1_TO,
    10,
    { a: (date) => frac(Number(date.slice(8, 10)), 7), b: () => frac(2, 5) },
    (date, metric) => date === '2025-03-20' && metric === 'b',
  );
  // 追溯到季初的整套权重，随后又被撤销
  ledger.addRevision({ seq: 200, ruleId: 'retro', validFrom: Q1_FROM, validTo: Q1_TO, weights: { a: frac(1, 5), b: frac(4, 5) } });
  ledger.addRevocation(201, 200);
  // 跨月局部修订
  ledger.addRevision({
    seq: 202,
    ruleId: 'patch',
    validFrom: '2025-02-14',
    validTo: '2025-03-03',
    weights: { a: frac(3, 4), b: frac(1, 4) },
  });
  // 批量补录：覆盖旧分、补齐缺分、追加 3 月上半月的新规则
  ledger.ingestBatch([
    { kind: 'score', seq: 300, date: '2025-01-15', metric: 'a', value: frac(7, 9) },
    { kind: 'score', seq: 301, date: '2025-03-20', metric: 'b', value: frac(1, 11) },
    { kind: 'revision', seq: 302, ruleId: 'march', validFrom: '2025-03-01', validTo: '2025-03-15', weights: { a: frac(1) } },
  ]);

  const assertBothPathsEqual = (asOfSeq?: number): void => {
    const cached = ledger.queryQuarter(Q1_FROM, Q1_TO, asOfSeq);
    const scanned = ledger.queryQuarterScan(Q1_FROM, Q1_TO, asOfSeq);
    assert.deepEqual(cached, scanned, `asOfSeq=${asOfSeq ?? 'current'}`);
  };

  assertBothPathsEqual(150);
  assertBothPathsEqual(200);
  assertBothPathsEqual(undefined);

  const atRetro = ledger.queryQuarter(Q1_FROM, Q1_TO, 200);
  assert.deepEqual(atRetro.missing, [{ date: '2025-03-20', metrics: ['b'] }]);

  const current = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.deepEqual(current.missing, [], '补录后 2025-03-20 已完整');
  assert.deepEqual(current.segments, [
    { from: '2025-01-01', to: '2025-02-14', ruleId: 'base', ruleSeq: 1 },
    { from: '2025-02-14', to: '2025-03-01', ruleId: 'patch', ruleSeq: 202 },
    { from: '2025-03-01', to: '2025-03-15', ruleId: 'march', ruleSeq: 302 },
    { from: '2025-03-15', to: '2025-04-01', ruleId: 'base', ruleSeq: 1 },
  ]);

  // 继续演进：撤销 patch、再补一条分数，缓存路径仍与扫描一致
  ledger.ingestBatch([
    { kind: 'revocation', seq: 400, targetSeq: 202 },
    { kind: 'score', seq: 401, date: '2025-02-20', metric: 'b', value: frac(3, 13) },
  ]);
  assertBothPathsEqual(undefined);
  assertBothPathsEqual(202);

  const afterRevoke = ledger.queryQuarter(Q1_FROM, Q1_TO);
  assert.deepEqual(afterRevoke.segments, [
    { from: '2025-01-01', to: '2025-03-01', ruleId: 'base', ruleSeq: 1 },
    { from: '2025-03-01', to: '2025-03-15', ruleId: 'march', ruleSeq: 302 },
    { from: '2025-03-15', to: '2025-04-01', ruleId: 'base', ruleSeq: 1 },
  ]);
});
