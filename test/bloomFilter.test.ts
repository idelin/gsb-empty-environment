import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BloomFilter } from '../src/bloomFilter.ts';
import { mulberry32, randomHashKey } from '../src/scenario.ts';

test('已添加的键绝不漏判（无 false negative）', () => {
  const rng = mulberry32(42);
  const filter = new BloomFilter(1000, 10);
  const keys: string[] = [];
  for (let i = 0; i < 1000; i++) {
    const key = randomHashKey(rng);
    keys.push(key);
    filter.add(key);
  }
  for (const key of keys) {
    assert.equal(filter.mightContain(key), true);
  }
});

test('误判率维持在理论上界内（10 bits/key → <3%）', () => {
  const rng = mulberry32(43);
  const filter = new BloomFilter(1000, 10);
  const inserted = new Set<string>();
  for (let i = 0; i < 1000; i++) {
    const key = randomHashKey(rng);
    inserted.add(key);
    filter.add(key);
  }
  let falsePositives = 0;
  const probes = 20000;
  for (let i = 0; i < probes; i++) {
    const key = randomHashKey(rng);
    if (inserted.has(key)) continue;
    if (filter.mightContain(key)) falsePositives++;
  }
  const rate = falsePositives / probes;
  assert.ok(rate > 0, '应存在少量误判');
  assert.ok(rate < 0.03, `误判率 ${rate} 应小于 3%`);
});

test('提高 bitsPerKey 可降低误判率', () => {
  const measure = (bitsPerKey: number): number => {
    const rng = mulberry32(44);
    const filter = new BloomFilter(500, bitsPerKey);
    const inserted = new Set<string>();
    for (let i = 0; i < 500; i++) {
      const key = randomHashKey(rng);
      inserted.add(key);
      filter.add(key);
    }
    let fp = 0;
    for (let i = 0; i < 20000; i++) {
      const key = randomHashKey(rng);
      if (!inserted.has(key) && filter.mightContain(key)) fp++;
    }
    return fp / 20000;
  };
  assert.ok(measure(14) < measure(6));
});
