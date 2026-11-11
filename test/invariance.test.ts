import assert from 'node:assert/strict';
import test from 'node:test';
import { tokenize } from '../src/parser/tokenizer.ts';
import { buildAndVerify, cmp, col, param, select, table } from '../index.ts';
import { whitelist } from './fixtures.ts';

test('probe tokenizer recognizes sentinel literals including empty content', () => {
  const tokens = tokenize('$$a$$ $$$$ $$1 OR 1=1$$');
  assert.deepEqual(
    tokens.map((token) => token.type),
    ['probe', 'probe', 'probe'],
  );
  assert.deepEqual(
    tokens.map((token) => token.value),
    ['a', '', '1 OR 1=1'],
  );
});

test('every parameter slot is exercised by adversarial replacement probes', () => {
  const q = select({
    selectList: [col('id')],
    from: table('users'),
    where: cmp(
      '=',
      col('name'),
      param('name', 'string'),
    ),
  });
  const report = buildAndVerify(q, whitelist);
  assert.equal(report.checks.parameterInvariance, 'passed');
  assert.equal(report.slots.length, 1);
  assert.equal(report.slots[0].paramType, 'string');
});

test('integer and null typed parameters are covered by the probe matrix', () => {
  const q = select({
    selectList: [col('id')],
    from: table('users'),
    where: cmp('=', col('tenant_id'), param('tenant', 'integer')),
  });
  const report = buildAndVerify(q, whitelist);
  assert.equal(report.checks.parameterInvariance, 'passed');
  assert.equal(report.slots[0].paramType, 'integer');
});
