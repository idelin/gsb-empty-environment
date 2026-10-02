import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { BloomFilter } from '../src/bloomFilter.ts';
import { LsmEngine, type GetTrace } from '../src/lsmEngine.ts';
import { LsmDiagnosticsMonitor, type Alert } from '../src/monitor.ts';

/** 确定性伪随机（LCG），避免测试抖动 */
function seededRandom(seed: number): () => number {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

function hashKey(i: number): string {
  return createHash('sha256').update(`key-${i}`).digest('hex').slice(0, 32);
}

function buildEngine(tableCount = 8, perTable = 64) {
  const engine = new LsmEngine({
    memtableFlushThreshold: perTable,
    compactionTrigger: 1000,
  });
  const keys: string[] = [];
  for (let i = 0; i < tableCount * perTable; i++) {
    const k = hashKey(i);
    keys.push(k);
    engine.put(k, `v${i}`);
  }
  return { engine, keys };
}

test('BloomFilter: 已添加的 key 绝不漏报（无假阴性）', () => {
  const bf = new BloomFilter(1000, 0.01);
  const keys = Array.from({ length: 1000 }, (_, i) => hashKey(i));
  for (const k of keys) bf.add(k);
  for (const k of keys) assert.ok(bf.mightContain(k), `false negative for ${k}`);
});

test('BloomFilter: 误判率维持在目标附近', () => {
  const bf = new BloomFilter(1000, 0.01);
  for (let i = 0; i < 1000; i++) bf.add(hashKey(i));
  let fp = 0;
  const trials = 10000;
  for (let i = 0; i < trials; i++) {
    if (bf.mightContain(hashKey(100000 + i))) fp++;
  }
  assert.ok(fp / trials < 0.05, `fp rate ${fp / trials} too high`);
});

test('引擎: Bloom 全开时读放大 ≈ 1（基线 1.05 以下）', () => {
  const { engine, keys } = buildEngine();
  assert.equal(engine.sstableCount, 8);
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 500, minSamples: 20 });
  monitor.attach(engine);
  for (let i = 0; i < 500; i++) engine.get(keys[i % keys.length]);
  const s = monitor.stats();
  assert.ok(s.readAmplification < 1.1, `baseline read amp ${s.readAmplification}`);
  assert.equal(monitor.recentAlerts.length, 0);
});

test('验收: 关闭部分 Bloom Filter 后读放大跃升至 ~4.2 并触发 Compaction 告警', () => {
  const { engine, keys } = buildEngine();
  const monitor = new LsmDiagnosticsMonitor({
    windowSize: 200,
    readAmpThreshold: 2.0,
    minSamples: 20,
  });
  monitor.attach(engine);
  const alerts: Alert[] = [];
  monitor.onAlert((a) => alerts.push(a));

  // 基线
  const rand = seededRandom(42);
  for (let i = 0; i < 200; i++) engine.get(keys[Math.floor(rand() * keys.length)]);
  const baseline = monitor.stats().readAmplification;
  assert.ok(baseline < 1.1, `baseline ${baseline}`);

  // 故意关闭部分 Bloom Filter（仅保留最旧一张）
  const disabled = engine.disableBloomOnSome((_, idx) => idx <= 6);
  assert.equal(disabled, 7);
  monitor.reset();
  for (let i = 0; i < 200; i++) engine.get(keys[Math.floor(rand() * keys.length)]);
  const after = monitor.stats().readAmplification;

  // 读放大从 ~1.05 跃升至 ~4.2（允许 ±0.6 抖动）
  assert.ok(after > 3.6 && after < 5.0, `read amp jumped to ${after}`);
  assert.ok(after / baseline > 3, `amplification ratio ${after / baseline}`);

  // 监控即时告警，且建议包含提高 Compaction 权重
  const readAmpAlert = alerts.find((a) => a.kind === 'READ_AMPLIFICATION');
  assert.ok(readAmpAlert, 'expected READ_AMPLIFICATION alert');
  assert.match(readAmpAlert!.suggestion, /Compaction/);
  assert.match(readAmpAlert!.message, /读放大/);
});

test('监控: Bloom 误判率越限时建议重建 Filter', () => {
  const monitor = new LsmDiagnosticsMonitor({
    windowSize: 100,
    bloomFpThreshold: 0.05,
    readAmpThreshold: 100, // 屏蔽读放大告警
    minSamples: 10,
  });
  const trace: GetTrace = {
    key: 'k',
    found: false,
    tablesRead: 1,
    bloomChecks: 10,
    bloomFalsePositives: 2, // 20% 误判率
  };
  for (let i = 0; i < 20; i++) monitor.record(trace);
  const alert = monitor.recentAlerts.find((a) => a.kind === 'BLOOM_FALSE_POSITIVE');
  assert.ok(alert, 'expected BLOOM_FALSE_POSITIVE alert');
  assert.match(alert!.suggestion, /重建 Bloom Filter/);
});

test('修复闭环: 重建 Filter + 提高 Compaction 权重后读放大回落', () => {
  const { engine, keys } = buildEngine();
  const monitor = new LsmDiagnosticsMonitor({ windowSize: 200, minSamples: 20 });
  monitor.attach(engine);

  engine.disableBloomOnSome(() => true);
  for (let i = 0; i < 200; i++) engine.get(keys[i % keys.length]);
  const degraded = monitor.stats().readAmplification;
  assert.ok(degraded > 3.0, `degraded ${degraded}`);

  // 采纳建议
  engine.rebuildAllFilters();
  engine.compactionFanIn = 8;
  while (engine.sstableCount > 1) engine.compact();
  assert.equal(engine.sstableCount, 1);

  monitor.reset();
  for (let i = 0; i < 200; i++) engine.get(keys[i % keys.length]);
  const recovered = monitor.stats().readAmplification;
  assert.ok(recovered <= 1.0, `recovered ${recovered}`);
  assert.equal(monitor.recentAlerts.length, 0);

  // 数据在 compaction 后仍然正确
  for (let i = 0; i < keys.length; i += 97) {
    assert.equal(engine.get(keys[i]), `v${i}`);
  }
});

test('引擎: 覆盖写后 compaction 保留最新值', () => {
  const engine = new LsmEngine({ memtableFlushThreshold: 4, compactionTrigger: 1000 });
  for (let round = 0; round < 3; round++) {
    for (let i = 0; i < 4; i++) engine.put(hashKey(i), `round${round}`);
    engine.flush();
  }
  engine.compactionFanIn = 3;
  engine.compact();
  assert.equal(engine.sstableCount, 1);
  for (let i = 0; i < 4; i++) assert.equal(engine.get(hashKey(i)), 'round2');
});
