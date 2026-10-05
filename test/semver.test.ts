import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compareVersions, satisfies } from '../src/semver.ts';

test('版本比较', () => {
  assert.ok(compareVersions('1.2.3', '1.2.10') < 0);
  assert.ok(compareVersions('2.0.0', '10.0.0') < 0);
  assert.ok(compareVersions('1.0.0-alpha', '1.0.0') < 0);
  assert.equal(compareVersions('v1.2.3', '1.2.3'), 0);
});

test('范围匹配', () => {
  assert.ok(satisfies('1.2.3', '*'));
  assert.ok(satisfies('1.2.3', '1.2.3'));
  assert.ok(!satisfies('1.2.3', '2.0.0'));
  assert.ok(satisfies('1.5.0', '1.x'));
  assert.ok(satisfies('1.2.9', '1.2.x'));
  assert.ok(!satisfies('1.3.0', '1.2.x'));
  assert.ok(satisfies('1.2.3', '^1.0.0'));
  assert.ok(!satisfies('2.0.0', '^1.0.0'));
  assert.ok(satisfies('0.2.3', '^0.2.0'));
  assert.ok(!satisfies('0.3.0', '^0.2.0'));
  assert.ok(satisfies('1.2.3', '~1.2.0'));
  assert.ok(!satisfies('1.3.0', '~1.2.0'));
  assert.ok(satisfies('1.5.0', '>=1.0.0 <2.0.0'));
  assert.ok(!satisfies('2.0.0', '>=1.0.0 <2.0.0'));
  assert.ok(satisfies('2.0.0', '^1.0.0 || ^2.0.0'));
});
