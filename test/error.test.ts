import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { loadInstance } from '../src/model.ts';
import { evaluateError, checkConstraints, selectionCost } from '../src/error.ts';
import { parseDecimal, toExactString, cmp } from '../src/rational.ts';

const tiny = loadInstance(JSON.parse(readFileSync(new URL('../samples/tiny.json', import.meta.url), 'utf8')));
const demo = loadInstance(JSON.parse(readFileSync(new URL('../samples/demo.json', import.meta.url), 'utf8')));

test('shared source: correlated signed sum, NOT independent sum of absolutes', () => {
  // a=precise (cal +1.5*0.005=+0.0075), b=cheap (cal -1.0*0.015=-0.015)
  const sel = new Map([['a', 'precise'], ['b', 'cheap']]);
  const report = evaluateError(tiny, sel);
  const cal = report.perSource.find((s) => s.sourceId === 'cal')!;
  assert.equal(toExactString(cal.signedSum), '-3/400'); // -0.0075
  assert.equal(toExactString(cal.worstCase), '3/400'); // |signed sum|
  assert.equal(toExactString(cal.independentSum), '9/400'); // naive sum, contrast only
  assert.ok(cmp(cal.worstCase, cal.independentSum) < 0, 'correlated bound is tighter');
});

test('independent source: negative sensitivity counts by absolute value', () => {
  // tiny: a has sensitivity -0.5 to n_a; worst case uses |a|*e
  const sel = new Map([['a', 'cheap'], ['b', 'cheap']]);
  const report = evaluateError(tiny, sel);
  const na = report.perSource.find((s) => s.sourceId === 'n_a')!;
  assert.equal(toExactString(na.worstCase), '1/100'); // 0.5 * 0.02
});

test('total is sum of per-source worst cases; feasibility is exact', () => {
  const sel = new Map([['a', 'precise'], ['b', 'cheap']]);
  const report = evaluateError(tiny, sel);
  // cal 3/400 + n_a 0.5*0.005=1/400 + n_b 0.8*0.015=3/250 -> 0.0075+0.0025+0.012
  assert.equal(toExactString(report.total), '11/500');
  assert.ok(report.feasible);
});

test('cheapest tier everywhere can exceed the budget', () => {
  const cheapest = new Map(demo.components.map((c) => [c.id, c.tiers[0].id]));
  const report = evaluateError(demo, cheapest);
  assert.ok(!report.feasible, 'all-cheapest selection must be infeasible in the demo');
  assert.equal(toExactString(report.total), '349/5000'); // 0.0698 > 0.05
});

test('exclusions and dependencies are detected', () => {
  const badExclusion = new Map([
    ['sensor', 'low'], ['amp', 'low'], ['adc', 'high'], ['ref', 'low'],
  ]);
  assert.ok(checkConstraints(demo, badExclusion).some((v) => v.startsWith('exclusion')));
  const badDependency = new Map([
    ['sensor', 'mid'], ['amp', 'high'], ['adc', 'low'], ['ref', 'mid'],
  ]);
  assert.ok(checkConstraints(demo, badDependency).some((v) => v.startsWith('dependency')));
  const ok = new Map([
    ['sensor', 'mid'], ['amp', 'high'], ['adc', 'low'], ['ref', 'high'],
  ]);
  assert.deepEqual(checkConstraints(demo, ok), []);
});

test('selection cost is exact', () => {
  const sel = new Map([['a', 'precise'], ['b', 'cheap']]);
  assert.ok(cmp(selectionCost(tiny, sel), parseDecimal('13')) === 0);
});
