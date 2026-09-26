import assert from 'node:assert/strict';
import { test } from 'node:test';

import { ValidationCache } from '../src/cache.ts';
import { validate } from '../src/validator.ts';

test('缓存键绑定 schema 摘要与样本摘要', () => {
  const schema = { type: 'integer' };
  const first = ValidationCache.keyFor(schema, 1);
  const second = ValidationCache.keyFor(schema, 1);
  assert.equal(first.cacheKey, second.cacheKey);
  assert.equal(first.cacheKey, `${first.schemaDigest}#${first.sampleDigest}`);
  assert.notEqual(ValidationCache.keyFor(schema, 2).cacheKey, first.cacheKey);
  assert.notEqual(ValidationCache.keyFor({ type: 'string' }, 1).cacheKey, first.cacheKey);
});

test('相同输入只计算一次，再次访问命中', () => {
  let computations = 0;
  const cache = new ValidationCache((schema, sample) => {
    computations += 1;
    return validate(schema, sample);
  });
  const schema = { type: 'integer' };
  const first = cache.validate(schema, 1);
  const second = cache.validate(schema, 1);
  assert.equal(first.fromCache, false);
  assert.equal(second.fromCache, true);
  assert.equal(computations, 1);
  assert.equal(cache.stats.hits, 1);
  assert.equal(cache.stats.misses, 1);
});

test('样本改变后不复用旧的通过结果，必须重算', () => {
  const cache = new ValidationCache((schema, sample) => validate(schema, sample));
  const schema = { type: 'integer', minimum: 1 };

  const before = cache.validate(schema, 5);
  assert.equal(before.result.valid, true);

  const changedSample = cache.validate(schema, 0);
  assert.equal(changedSample.fromCache, false);
  assert.notEqual(changedSample.cacheKey, before.cacheKey);
  assert.equal(changedSample.result.valid, false);
  assert.equal(changedSample.result.errors[0].schemaPath, '/minimum');
});

test('schema 改变后不复用旧结果', () => {
  const cache = new ValidationCache((schema, sample) => validate(schema, sample));
  const before = cache.validate({ type: 'integer' }, 1);
  assert.equal(before.result.valid, true);

  const changedSchema = cache.validate({ type: 'string' }, 1);
  assert.equal(changedSchema.fromCache, false);
  assert.notEqual(changedSchema.cacheKey, before.cacheKey);
  assert.equal(changedSchema.result.valid, false);
});
