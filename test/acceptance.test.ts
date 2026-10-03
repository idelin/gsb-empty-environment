import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildScenario, mulberry32, sampleKeys } from '../src/scenario.ts';
import type { Alert } from '../src/monitor.ts';

// 验收场景：写入随机散列 Key → 故意关闭部分 Bloom Filter →
// 监控即时捕捉读放大跃升（≈1.05 → ≈4.2）并给出 Compaction 建议。
test('验收：关闭部分 Bloom Filter 后读放大跃升并触发 Compaction 告警', () => {
  const { engine, monitor, allKeys } = buildScenario();
  assert.equal(engine.tableCount, 9);
  assert.equal(engine.disabledFilterCount, 0);

  // 阶段 A：全部 Filter 启用
  const rngA = mulberry32(1);
  let readsA = 0;
  for (const key of sampleKeys(allKeys, 3000, rngA)) {
    readsA += engine.getWithTrace(key).trace.sstablesRead;
    assert.equal(engine.drainAlerts().length, 0, '健康阶段不应有告警');
  }
  const avgA = readsA / 3000;
  assert.ok(avgA >= 1.0 && avgA < 1.3, `阶段 A 读放大 ${avgA} 应接近 1.05`);

  // 故障注入：故意关闭最旧 7 张表的 Bloom Filter
  for (const table of engine.getTables().slice(2)) table.disableBloomFilter();
  assert.equal(engine.disabledFilterCount, 7);

  // 阶段 B：监控应即时捕捉跃升
  const rngB = mulberry32(2);
  let readsB = 0;
  let alertAt = -1;
  const fired: Alert[] = [];
  for (let i = 0; i < 3000; i++) {
    readsB += engine.getWithTrace(allKeys[Math.floor(rngB() * allKeys.length)]).trace.sstablesRead;
    for (const alert of engine.drainAlerts()) {
      if (alertAt < 0) alertAt = i + 1;
      fired.push(alert);
    }
  }
  const avgB = readsB / 3000;
  assert.ok(avgB > 3.5 && avgB < 5.0, `阶段 B 读放大 ${avgB} 应跃升至 ≈4.2`);
  assert.ok(avgB > avgA * 3, '读放大应出现数倍跃升');

  assert.ok(alertAt > 0 && alertAt <= 300, `监控应在 300 次查询内捕捉跃升（实际第 ${alertAt} 次）`);
  const compactionAlert = fired.find((a) => a.code === 'HIGH_READ_AMPLIFICATION');
  assert.ok(compactionAlert, '应触发 HIGH_READ_AMPLIFICATION 告警');
  assert.match(compactionAlert.suggestion, /Compaction/);
  assert.match(compactionAlert.suggestion, /读延迟/);

  // 处置：按建议 Compaction + 重建 Filter 后恢复
  engine.compactAll();
  assert.equal(engine.tableCount, 1);
  assert.equal(engine.disabledFilterCount, 0);
  monitor.reset();
  const rngC = mulberry32(3);
  let readsC = 0;
  for (const key of sampleKeys(allKeys, 3000, rngC)) {
    readsC += engine.getWithTrace(key).trace.sstablesRead;
  }
  assert.ok(readsC / 3000 <= 1.2, 'Compaction 后读放大应回落');
});
