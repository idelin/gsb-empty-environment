import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LsmEngine } from '../src/engine.ts';
import { mulberry32, randomHashKey } from '../src/scenario.ts';

function buildEngine(tableCount: number, keysPerTable: number) {
  const engine = new LsmEngine({ bitsPerKey: 10 });
  const rng = mulberry32(7);
  const allKeys: string[] = [];
  for (let t = 0; t < tableCount; t++) {
    for (let i = 0; i < keysPerTable; i++) {
      const key = randomHashKey(rng);
      engine.put(key, `val-${key}`);
      allKeys.push(key);
    }
    engine.flush();
  }
  return { engine, allKeys };
}

test('写入后可读回，且新值覆盖旧值', () => {
  const engine = new LsmEngine();
  engine.put('k1', 'v1');
  engine.flush();
  engine.put('k1', 'v2');
  engine.put('k2', 'v3');
  engine.flush();
  assert.equal(engine.get('k1'), 'v2');
  assert.equal(engine.get('k2'), 'v3');
  assert.equal(engine.get('missing'), undefined);
});

test('Bloom Filter 启用时读放大接近 1，关闭后显著上升', () => {
  const { engine, allKeys } = buildEngine(6, 300);

  let readsWithFilter = 0;
  for (const key of allKeys) readsWithFilter += engine.getWithTrace(key).trace.sstablesRead;
  const avgWith = readsWithFilter / allKeys.length;
  assert.ok(avgWith < 1.5, `启用 Filter 时平均读放大 ${avgWith} 应 < 1.5`);

  for (const table of engine.getTables()) table.disableBloomFilter();
  let readsWithout = 0;
  for (const key of allKeys) readsWithout += engine.getWithTrace(key).trace.sstablesRead;
  const avgWithout = readsWithout / allKeys.length;
  assert.ok(avgWithout > 2.5, `关闭 Filter 后平均读放大 ${avgWithout} 应 > 2.5`);
  assert.ok(avgWithout > avgWith * 2);
});

test('追踪指标：Bloom 否定不计入读放大，误判计入 falsePositives', () => {
  const { engine, allKeys } = buildEngine(4, 200);
  const { trace } = engine.getWithTrace(allKeys[0]);
  assert.equal(trace.found, true);
  assert.ok(trace.sstablesRead >= 1);
  assert.equal(trace.sstablesRead + trace.bloomNegatives + trace.skippedByRange <= 4, true);
  assert.ok(trace.falsePositives <= trace.sstablesRead);
});

test('compactAll 合并为单张 SSTable 且数据不丢失', () => {
  const { engine, allKeys } = buildEngine(5, 200);
  assert.equal(engine.tableCount, 5);
  engine.compactAll();
  assert.equal(engine.tableCount, 1);
  for (const key of allKeys) {
    assert.equal(engine.get(key), `val-${key}`);
  }
  const { trace } = engine.getWithTrace(allKeys[0]);
  assert.equal(trace.sstablesRead, 1);
});
