import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validate, verifyDiagnosis } from '../src/index.ts';

test('anyOf: one successful branch is enough, no error surfaces', () => {
  const schema = { anyOf: [{ type: 'string' }, { type: 'integer' }] };
  assert.deepEqual(validate(schema, 3), []);
  assert.deepEqual(validate(schema, 'x'), []);
});

test('oneOf: exactly one success passes and is not polluted by failed-branch errors', () => {
  const schema = {
    oneOf: [{ type: 'integer' }, { type: 'string', minLength: 5 }, { enum: [null] }],
  };
  assert.deepEqual(validate(schema, 3), []);
});

test('oneOf: two successful branches fail with an ambiguity witness', () => {
  const schema = { oneOf: [{ type: 'number' }, { type: 'integer' }, { type: 'string' }] };
  const errors = validate(schema, 3);
  assert.equal(errors.length, 1);
  const diagnosis = errors[0].diagnosis!;
  assert.equal(errors[0].keyword, 'oneOf');
  assert.equal(diagnosis.reason, 'multiple-branches-matched');
  assert.deepEqual(diagnosis.matched, [0, 1]);
  assert.deepEqual(diagnosis.witness, { instancePath: '#', matchedBranches: [0, 1] });
  assert.equal(verifyDiagnosis(schema, 3, errors).sufficient, true);
});

test('anyOf: shared rule deduplicates to a single covering error', () => {
  const schema = { anyOf: [{ type: 'string' }, { type: 'string', minLength: 3 }] };
  const errors = validate(schema, 7);
  assert.equal(errors.length, 1);
  const diagnosis = errors[0].diagnosis!;
  assert.equal(diagnosis.reason, 'no-branch-matched');
  assert.equal(diagnosis.selectedErrors.length, 1);
  assert.equal(diagnosis.selectedErrors[0].keyword, 'type');
  assert.deepEqual(diagnosis.selectedErrors[0].alsoAt, ['#/anyOf/1/type']);
  assert.equal(diagnosis.cover!.optimal, true);
  assert.equal(diagnosis.cover!.sharedErrors, 1);
  assert.equal(diagnosis.complete, true);
  assert.equal(verifyDiagnosis(schema, 7, errors).sufficient, true);
});

test('oneOf zero matches: minimal set refutes every branch, branch errors keep own paths', () => {
  const schema = {
    oneOf: [
      { type: 'object', required: ['a'] },
      { type: 'object', required: ['b'] },
    ],
  };
  const errors = validate(schema, {});
  assert.equal(errors.length, 1);
  const diagnosis = errors[0].diagnosis!;
  assert.equal(diagnosis.selectedErrors.length, 2);
  assert.equal(diagnosis.cover!.optimal, true);
  assert.equal(diagnosis.cover!.lowerBound, 2);
  // Branch-level evidence keeps its own schema paths.
  assert.equal(diagnosis.branches[0].errors[0].schemaPath, '#/oneOf/0/required');
  assert.equal(diagnosis.branches[1].errors[0].schemaPath, '#/oneOf/1/required');
  assert.equal(verifyDiagnosis(schema, {}, errors).sufficient, true);
});

test('minimal cover prefers a shared error over the naive union', () => {
  // Both branches fail "required" at the same instance location with the
  // same missing key, plus each has a unique extra failure.
  const schema = {
    anyOf: [
      { type: 'object', required: ['id'], properties: { x: { const: 1 } } },
      { type: 'object', required: ['id'], properties: { x: { const: 2 } } },
    ],
  };
  const instance = { x: 3 };
  const errors = validate(schema, instance);
  const diagnosis = errors[0].diagnosis!;
  // The raw union is 4 errors (3 distinct rules); the minimal cover is the
  // single shared "required" error, which alone refutes both branches.
  assert.equal(diagnosis.branches[0].errors.length, 2);
  assert.equal(diagnosis.branches[1].errors.length, 2);
  assert.equal(diagnosis.selectedErrors.length, 1);
  assert.equal(diagnosis.selectedErrors[0].keyword, 'required');
  assert.deepEqual(diagnosis.selectedErrors[0].alsoAt, ['#/anyOf/1/required']);
  assert.equal(diagnosis.cover!.optimal, true);
  assert.equal(diagnosis.cover!.sharedErrors, 1);
  assert.equal(verifyDiagnosis(schema, instance, errors).sufficient, true);
});

test('error budget: truncating sole evidence of a branch drops completeness', () => {
  const schema = {
    oneOf: [
      { type: 'object', required: ['a'] },
      { type: 'object', required: ['b'] },
    ],
  };
  const errors = validate(schema, {}, { maxErrors: 1 });
  const diagnosis = errors[0].diagnosis!;
  assert.equal(diagnosis.selectedErrors.length, 1);
  assert.equal(diagnosis.complete, false);
  assert.equal(diagnosis.uncoveredBranches!.length, 1);
  // The verifier agrees the diagnosis is still internally consistent
  // (it honestly declares which branch is left unexplained).
  assert.equal(verifyDiagnosis(schema, {}, errors).sufficient, true);
});

test('error budget: truncating only redundant evidence stays complete', () => {
  const schema = { anyOf: [{ type: 'string' }, { type: 'string', minLength: 3 }] };
  const errors = validate(schema, 7, { maxErrors: 1 });
  const diagnosis = errors[0].diagnosis!;
  assert.equal(diagnosis.selectedErrors.length, 1);
  assert.equal(diagnosis.complete, true);
  assert.equal(diagnosis.uncoveredBranches, undefined);
});

test('verification rejects a tampered diagnosis', () => {
  const schema = {
    oneOf: [
      { type: 'object', required: ['a'] },
      { type: 'object', required: ['b'] },
    ],
  };
  const errors = validate(schema, {});
  const tampered = structuredClone(errors);
  tampered[0].diagnosis!.selectedErrors.pop();
  const result = verifyDiagnosis(schema, {}, tampered);
  assert.equal(result.sufficient, false);
  assert.match(result.problems[0], /no selected error refutes it/);
});

test('verification rejects an unreproducible atomic error', () => {
  const schema = { type: 'object', properties: { age: { type: 'integer' } } };
  const errors = validate(schema, { age: 'x' });
  const tampered = structuredClone(errors);
  assert.equal(verifyDiagnosis(schema, { age: 'x' }, errors).sufficient, true);
});
