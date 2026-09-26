import assert from 'node:assert/strict';
import { test } from 'node:test';

import { classify, rulePaths } from '../src/compare.ts';
import type { ValidationError, ValidationResult } from '../src/validator.ts';

function error(schemaPath: string, message = 'mismatch'): ValidationError {
  return { instancePath: '/x', schemaPath, keyword: schemaPath.split('/').at(-1)!, message };
}

function result(errors: ValidationError[]): ValidationResult {
  return { valid: errors.length === 0, errors };
}

const pass = result([]);
const failA = result([error('/properties/a/type')]);
const failASamePathDifferentMessage = result([
  error('/properties/a/type', 'different message text'),
]);
const failB = result([error('/properties/b/pattern')]);

test('五类分类', () => {
  assert.equal(classify(pass, pass), 'still-passing');
  assert.equal(classify(pass, failA), 'new-failure');
  assert.equal(classify(failA, pass), 'fixed');
  assert.equal(classify(failA, failASamePathDifferentMessage), 'still-failing');
  assert.equal(classify(failA, failB), 'reason-changed');
});

test('规则路径以集合比较，与错误文案无关', () => {
  assert.deepEqual(rulePaths(failA.errors), ['/properties/a/type']);
  const multiOld = result([error('/a'), error('/b')]);
  const multiNew = result([error('/b'), error('/a')]);
  assert.equal(classify(multiOld, multiNew), 'still-failing');
  const multiOther = result([error('/a'), error('/c')]);
  assert.equal(classify(multiOld, multiOther), 'reason-changed');
});
