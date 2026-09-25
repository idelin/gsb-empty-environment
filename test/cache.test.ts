import { test } from 'node:test';
import assert from 'node:assert/strict';
import { DecisionCache } from '../src/cache.ts';

function makeCache(ttlMs = 1000) {
  let now = 10_000;
  const cache = new DecisionCache({ ttlMs, now: () => now });
  return { cache, advance: (ms: number) => { now += ms; } };
}

test('命中：条目携带着实际读取属性、规则版本与时间失效点', () => {
  const { cache } = makeCache();
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map([['subject:alice:role', 'admin']]));
  const hit = cache.get('docs', 'alice', 'web', 1);
  assert.equal(hit?.decision, 'allow');
  assert.equal(hit?.ruleVersion, 1);
  assert.equal(hit?.reads.get('subject:alice:role'), 'admin');
  assert.equal(hit?.expiresAt, 11_000);
});

test('时间失效点：过期条目不得命中并被清除', () => {
  const { cache, advance } = makeCache(500);
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map());
  advance(499);
  assert.equal(cache.get('docs', 'alice', 'web', 1)?.decision, 'allow');
  advance(1);
  assert.equal(cache.get('docs', 'alice', 'web', 1), undefined);
  assert.equal(cache.size, 0);
});

test('规则版本不一致不得命中', () => {
  const { cache } = makeCache();
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map());
  assert.equal(cache.get('docs', 'alice', 'web', 2), undefined);
});

test('缓存键包含主体与上下文：跨主体、跨上下文不串用', () => {
  const { cache } = makeCache();
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map());
  assert.equal(cache.get('docs', 'bob', 'web', 1), undefined);
  assert.equal(cache.get('docs', 'alice', 'mobile', 1), undefined);
});

test('属性失效：只波及实际读取过该属性的条目', () => {
  const { cache } = makeCache();
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map([['subject:alice:role', 'admin']]));
  cache.put('docs', 'bob', 'web', 'allow', 1, new Map([['subject:bob:role', 'admin']]));
  cache.put('docs', 'carol', 'web', 'deny', 1, new Map([['subject:carol:role', 'guest']]));
  assert.equal(cache.invalidateAttribute('subject:bob:role'), 1);
  assert.equal(cache.get('docs', 'alice', 'web', 1)?.decision, 'allow');
  assert.equal(cache.get('docs', 'bob', 'web', 1), undefined);
  assert.equal(cache.get('docs', 'carol', 'web', 1)?.decision, 'deny');
});

test('规则失效：清空该策略的全部条目', () => {
  const { cache } = makeCache();
  cache.put('docs', 'alice', 'web', 'allow', 1, new Map());
  cache.put('files', 'alice', 'web', 'allow', 1, new Map());
  assert.equal(cache.invalidatePolicy('docs'), 1);
  assert.equal(cache.size, 1);
  assert.equal(cache.get('files', 'alice', 'web', 1)?.decision, 'allow');
});
