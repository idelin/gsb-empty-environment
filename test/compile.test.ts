import { test } from 'node:test';
import assert from 'node:assert/strict';
import { compileMigration } from '../src/compile.ts';
import { MigrationError } from '../src/errors.ts';
import type { SchemaNode } from '../src/schema.ts';

const schema: SchemaNode = {
  type: 'object',
  fields: {
    depth: { type: 'number', unit: 'm' },
    count: { type: 'number', unit: 'mm', integer: true },
    label: { type: 'string' },
    readings: {
      type: 'array',
      items: {
        type: 'object',
        fields: {
          temp: { type: 'number', unit: 'degC' },
          spread: { type: 'number', unit: 'dC' },
        },
      },
    },
  },
};

test('unknown rule path is a compile error with the path', () => {
  assert.throws(
    () => compileMigration(schema, [{ path: 'missing.field', to: 'cm' }]),
    (err: unknown) => {
      assert.ok(err instanceof MigrationError);
      assert.equal(err.code, 'unknown-path');
      assert.equal(err.path, 'missing.field');
      return true;
    },
  );
});

test('rule bound to a non-numeric field is rejected', () => {
  assert.throws(
    () => compileMigration(schema, [{ path: 'label', to: 'cm' }]),
    (err: unknown) => (err as MigrationError).code === 'path-type-mismatch',
  );
});

test('dimension mismatch is caught at compile time', () => {
  assert.throws(
    () => compileMigration(schema, [{ path: 'depth', to: 'kg' }]),
    (err: unknown) => {
      const e = err as MigrationError;
      assert.equal(e.code, 'dimension-mismatch');
      assert.equal(e.path, 'depth');
      return true;
    },
  );
});

test('absolute temperature cannot migrate to a difference unit', () => {
  assert.throws(
    () => compileMigration(schema, [{ path: 'readings[].temp', to: 'dK' }]),
    (err: unknown) => (err as MigrationError).code === 'kind-mismatch',
  );
});

test('integer target without explicit rounding mode is rejected', () => {
  assert.throws(
    () => compileMigration(schema, [{ path: 'count', to: 'm' }]),
    (err: unknown) => (err as MigrationError).code === 'missing-rounding-mode',
  );
});

test('integer target accepts rounding from schema or rule', () => {
  const viaRule = compileMigration(schema, [{ path: 'count', to: 'm', rounding: 'trunc' }]);
  assert.equal(viaRule.paths[0]!.rounding, 'trunc');
  const schemaWithMode: SchemaNode = {
    type: 'object',
    fields: { count: { type: 'number', unit: 'mm', integer: true, rounding: 'half-even' } },
  };
  const viaSchema = compileMigration(schemaWithMode, [{ path: 'count', to: 'm' }]);
  assert.equal(viaSchema.paths[0]!.rounding, 'half-even');
});

test('consistent conversion cycle (m -> cm -> m) is accepted', () => {
  const plan = compileMigration(schema, [
    { path: 'depth', to: 'cm' },
    { path: 'depth', to: 'm' },
  ]);
  assert.equal(plan.paths[0]!.steps.length, 2);
  assert.equal(plan.paths[0]!.finalUnit.name, 'm');
});

test('contradictory conversion cycle is rejected with details', () => {
  // Two inline units share the name "half" but disagree on scale:
  // the cycle m -> half(2x) -> half(3x) does not compose to identity.
  assert.throws(
    () =>
      compileMigration(schema, [
        { path: 'depth', to: { name: 'half', dimension: 'length', scale: 2 } },
        { path: 'depth', to: { name: 'half', dimension: 'length', scale: 3 } },
      ]),
    (err: unknown) => {
      const e = err as MigrationError;
      assert.equal(e.code, 'contradictory-cycle');
      assert.equal(e.path, 'depth');
      assert.equal(e.details?.unit, 'half');
      return true;
    },
  );
});

test('multi-step chain compiles through arrays', () => {
  const plan = compileMigration(schema, [
    { path: 'readings[].temp', to: 'K' },
    { path: 'readings[].temp', to: 'degF' },
  ]);
  const p = plan.paths[0]!;
  assert.equal(p.steps.length, 2);
  assert.equal(p.sourceUnit.name, 'degC');
  assert.equal(p.finalUnit.name, 'degF');
});
