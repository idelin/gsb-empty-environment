import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { TemporalKpiLedger } from '../src/index.ts';

const KEYS = { '张伟': 'secret-key-zhangwei', '李娜': 'secret-key-lina' };

function buildQ1Ledger(): TemporalKpiLedger {
  const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
  ledger.recordActual('目标A', 108);
  ledger.recordActual('目标B', 72);
  ledger.commitVersion({
    author: '张伟',
    recordedAtDay: 1,
    reason: 'Q1 期初战略',
    entries: [
      { kpiId: '目标A', target: 100, weight: 0.6, validFrom: 1, validTo: 60 },
      { kpiId: '目标A', target: 100, weight: 0.5, validFrom: 60, validTo: 91 },
      { kpiId: '目标B', target: 80, weight: 0.4, validFrom: 1, validTo: 60 },
      { kpiId: '目标B', target: 80, weight: 0.5, validFrom: 60, validTo: 91 },
    ],
  });
  ledger.commitVersion({
    author: '李娜',
    recordedAtDay: 45,
    reason: '追溯调整：目标B 升为全季度主攻指标',
    entries: [
      { kpiId: '目标A', target: 100, weight: 0.4, validFrom: 1, validTo: 91 },
      { kpiId: '目标B', target: 80, weight: 0.6, validFrom: 1, validTo: 91 },
    ],
  });
  return ledger;
}

describe('时态指标账本：Q1 追溯调整验收场景', () => {
  it('调整前：v1 快照下季度末加权得分按分段天数加权', () => {
    const ledger = buildQ1Ledger();
    const report = ledger.computeScore(1);
    // [1,60): 0.6*1.08 + 0.4*0.9 = 1.008（59 天）；[60,91): 0.5*1.08 + 0.5*0.9 = 0.99（31 天）
    assert.equal(report.segments.length, 2);
    assert.ok(Math.abs(report.segments[0].score - 1.008) < 1e-9);
    assert.ok(Math.abs(report.segments[1].score - 0.99) < 1e-9);
    assert.ok(Math.abs(report.totalScore - (1.008 * 59 + 0.99 * 31) / 90) < 1e-9);
  });

  it('调整后：v2 追溯为全季度权重 A 0.4 / B 0.6', () => {
    const ledger = buildQ1Ledger();
    const report = ledger.computeScore(2);
    assert.equal(report.segments.length, 1);
    assert.ok(Math.abs(report.totalScore - (0.4 * 1.08 + 0.6 * 0.9)) < 1e-9);
  });

  it('输出调整前后实际得分差', () => {
    const ledger = buildQ1Ledger();
    const { base, target, delta } = ledger.compareVersions(1, 2);
    const expectedBase = (1.008 * 59 + 0.99 * 31) / 90; // 1.0018
    const expectedTarget = 0.972;
    assert.ok(Math.abs(base.totalScore - expectedBase) < 1e-9);
    assert.ok(Math.abs(target.totalScore - expectedTarget) < 1e-9);
    assert.ok(Math.abs(delta - (expectedTarget - expectedBase)) < 1e-9);
    assert.ok(delta < 0);
  });

  it('历史战略快照不受后续版本影响（考核推演可复现）', () => {
    const ledger = buildQ1Ledger();
    const before = ledger.computeScore(1).totalScore;
    ledger.commitVersion({
      author: '张伟',
      recordedAtDay: 80,
      reason: '季末微调',
      entries: [
        { kpiId: '目标A', target: 100, weight: 0.5, validFrom: 1, validTo: 91 },
        { kpiId: '目标B', target: 80, weight: 0.5, validFrom: 1, validTo: 91 },
      ],
    });
    assert.equal(ledger.computeScore(1).totalScore, before);
  });

  it('保留指标变更责任人版本签名，且可校验、防篡改', () => {
    const ledger = buildQ1Ledger();
    const versions = ledger.listVersions();
    assert.equal(versions.length, 2);
    assert.equal(versions[0].author, '张伟');
    assert.equal(versions[1].author, '李娜');
    assert.match(versions[0].signature, /^[0-9a-f]{64}$/);
    assert.notEqual(versions[0].signature, versions[1].signature);
    assert.ok(ledger.verifyVersion(1));
    assert.ok(ledger.verifyVersion(2));
    // 篡改权重后签名校验失败
    const tamperedVersion = { ...versions[1], entries: versions[1].entries.map((entry) => ({ ...entry })) };
    tamperedVersion.entries[0].weight = 0.9;
    const tampered = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    const internal = tampered as unknown as { versions: unknown[] };
    internal.versions.push(tamperedVersion);
    assert.equal(tampered.verifyVersion(2), false);
  });

  it('记录每次版本提交的得分影响', () => {
    const ledger = buildQ1Ledger();
    const impacts = ledger.impacts();
    assert.equal(impacts.length, 2);
    assert.equal(impacts[0].previousVersionId, null);
    assert.ok(Math.abs((impacts[0].newScore ?? 0) - (1.008 * 59 + 0.99 * 31) / 90) < 1e-9);
    assert.equal(impacts[1].previousVersionId, 1);
    assert.ok(Math.abs((impacts[1].delta ?? 0) - (0.972 - (1.008 * 59 + 0.99 * 31) / 90)) < 1e-9);
  });
});

describe('时态指标账本：校验与边界', () => {
  it('拒绝权重之和不等于 1 的版本', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    assert.throws(
      () =>
        ledger.commitVersion({
          author: '张伟',
          recordedAtDay: 1,
          reason: '权重缺失',
          entries: [{ kpiId: '目标A', target: 100, weight: 0.6, validFrom: 1, validTo: 91 }],
        }),
      /权重之和/,
    );
  });

  it('拒绝同一 KPI 有效区间重叠', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    assert.throws(
      () =>
        ledger.commitVersion({
          author: '张伟',
          recordedAtDay: 1,
          reason: '区间重叠',
          entries: [
            { kpiId: '目标A', target: 100, weight: 0.6, validFrom: 1, validTo: 60 },
            { kpiId: '目标A', target: 100, weight: 0.6, validFrom: 30, validTo: 91 },
            { kpiId: '目标B', target: 80, weight: 0.4, validFrom: 1, validTo: 91 },
          ],
        }),
      /重叠/,
    );
  });

  it('拒绝越出考核周期的有效区间', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    assert.throws(
      () =>
        ledger.commitVersion({
          author: '张伟',
          recordedAtDay: 1,
          reason: '越界',
          entries: [
            { kpiId: '目标A', target: 100, weight: 1, validFrom: 1, validTo: 92 },
          ],
        }),
      /越出考核周期/,
    );
  });

  it('未知责任人无法签名提交', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    assert.throws(
      () =>
        ledger.commitVersion({
          author: '王五',
          recordedAtDay: 1,
          reason: '无密钥',
          entries: [{ kpiId: '目标A', target: 100, weight: 1, validFrom: 1, validTo: 91 }],
        }),
      /未知责任人/,
    );
  });

  it('目标值随版本变化时按各自分段目标计算达成率', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    ledger.recordActual('目标A', 90);
    ledger.commitVersion({
      author: '张伟',
      recordedAtDay: 1,
      reason: '目标值分段',
      entries: [
        { kpiId: '目标A', target: 100, weight: 1, validFrom: 1, validTo: 46 },
        { kpiId: '目标A', target: 60, weight: 1, validFrom: 46, validTo: 91 },
      ],
    });
    const report = ledger.computeScore();
    // [1,46): 90/100=0.9（45 天）；[46,91): 90/60=1.5（45 天）
    assert.ok(Math.abs(report.totalScore - (0.9 * 45 + 1.5 * 45) / 90) < 1e-9);
  });

  it('缺少实际完成值时计分报错', () => {
    const ledger = new TemporalKpiLedger({ periodFrom: 1, periodTo: 91, keys: KEYS });
    ledger.commitVersion({
      author: '张伟',
      recordedAtDay: 1,
      reason: '期初',
      entries: [{ kpiId: '目标A', target: 100, weight: 1, validFrom: 1, validTo: 91 }],
    });
    assert.throws(() => ledger.computeScore(), /缺少 KPI 目标A 的实际完成值/);
  });
});
