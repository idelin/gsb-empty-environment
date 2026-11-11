import assert from 'node:assert/strict';
import test from 'node:test';
import { buildAndVerify, bindValues } from '../index.ts';
import { complexQuery, emptyInQuery, scalarSubqueryQuery, whitelist } from './fixtures.ts';

test('round-trips a complex query through renderer and independent parser', () => {
  const report = buildAndVerify(complexQuery(), whitelist);
  assert.equal(report.checks.renderRoundTrip, 'passed');
  assert.equal(report.checks.parameterInvariance, 'passed');
  assert.match(report.sql, /^SELECT DISTINCT/);
  assert.match(report.sql, /LEFT JOIN \(/);
  assert.match(report.sql, /EXISTS/);
  assert.match(report.sql, /GROUP BY/);
  assert.match(report.sql, /ORDER BY .* DESC, .* ASC/);
  assert.equal(report.sql.includes("'"), false);
});

test('deduplicates repeated parameters onto one slot', () => {
  const report = buildAndVerify(complexQuery(), whitelist);
  const statusSlots = report.slots.filter((slot) => slot.name === 'statusA');
  assert.equal(statusSlots.length, 1);
  const occurrences = report.sql.match(/\$\d+/g) ?? [];
  const distinct = new Set(occurrences);
  assert.ok(occurrences.length > distinct.size, 'repeated placeholder must reuse one slot');
});

test('normalizes empty IN sets into constant predicates without placeholders', () => {
  const report = buildAndVerify(emptyInQuery(), whitelist);
  assert.match(report.sql, /FALSE/);
  assert.match(report.sql, /\(NOT FALSE\)/);
  assert.equal(report.slots.length, 0);
  assert.equal(report.placeholders.length, 0);
});

test('binds typed values in slot order', () => {
  const report = buildAndVerify(complexQuery(), whitelist);
  const values = bindValues(report.slots, {
    tenantId: 3,
    defaultStatus: 'new',
    statusA: 'active',
    statusB: 'pending',
    minSpent: 12.5,
    lowId: 1,
    highId: 99,
    orderFloor: 0,
    minCount: 2,
  });
  assert.equal(values.length, report.slots.length);
  assert.equal(values[report.slots.findIndex((slot) => slot.name === 'tenantId')], 3);
});

test('rejects values that violate parameter types', () => {
  const report = buildAndVerify(complexQuery(), whitelist);
  assert.throws(
    () =>
      bindValues(report.slots, {
        tenantId: 1.5,
        defaultStatus: 'new',
        statusA: 'active',
        statusB: 'pending',
        minSpent: 12.5,
        lowId: 1,
        highId: 99,
        orderFloor: 0,
        minCount: 2,
      }),
    /safe integer/,
  );
});

test('supports schema-qualified tables inside scalar subqueries', () => {
  const report = buildAndVerify(scalarSubqueryQuery(), whitelist);
  assert.match(report.sql, /"billing"\."invoices"/);
  assert.equal(report.checks.subqueryScoping, 'passed');
});
