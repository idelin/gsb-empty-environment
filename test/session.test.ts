import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CompareSession,
  exactProvider,
  fixedIntervalProvider,
  halvingProvider,
  digitStreamProvider,
  parseRational,
  type CompareResult,
} from '../src/index.ts';

function decidedOutcome(r: CompareResult): string {
  assert.equal(r.kind, 'decided');
  return (r as { outcome: string }).outcome;
}

test('exact providers decide immediately with zero budget', () => {
  const s = new CompareSession(
    exactProvider('a', parseRational('1180591620717411303424')), // 2^70
    exactProvider('b', parseRational('1180591620717411303425')), // 2^70 + 1
    0,
  );
  assert.equal(decidedOutcome(s.run()), 'lt');
  assert.equal(s.budgetUsed, 0);
});

test('overlapping evidence refines within budget until separation', () => {
  // true values 1.000 vs 1.001, initial error ±0.01 halving each level
  const mk = () =>
    new CompareSession(
      halvingProvider('L', parseRational('1.000'), 'src-L', parseRational('0.01')),
      halvingProvider('R', parseRational('1.001'), 'src-R', parseRational('0.01')),
    );
  const tight = mk();
  tight.budgetUsed = 0;
  const smallBudget = new CompareSession(
    halvingProvider('L', parseRational('1.000'), 'src-L', parseRational('0.01')),
    halvingProvider('R', parseRational('1.001'), 'src-R', parseRational('0.01')),
    2,
  );
  const r1 = smallBudget.run();
  assert.equal(r1.kind, 'undetermined');
  assert.match((r1 as { reason: string }).reason, /budget exhausted/);

  const full = mk();
  const r2 = full.run();
  assert.equal(decidedOutcome(r2), 'lt');
  assert.ok(full.budgetUsed > 2, 'needed real refinement');
});

test('suspend and resume continues where the budget ran out', () => {
  const mkProviders = () => ({
    left: halvingProvider('L', parseRational('1.000'), 'src-L', parseRational('0.01')),
    right: halvingProvider('R', parseRational('1.001'), 'src-R', parseRational('0.01')),
  });

  const p1 = mkProviders();
  const first = new CompareSession(p1.left, p1.right, 2);
  const r1 = first.run();
  assert.equal(r1.kind, 'undetermined');
  const saved = first.suspend();

  // resume later with a topped-up budget; levels and spent budget carry over
  const p2 = mkProviders();
  const resumed = CompareSession.resume(saved, p2.left, p2.right, 50);
  assert.equal(resumed.leftLevel, first.leftLevel);
  assert.equal(resumed.rightLevel, first.rightLevel);
  assert.equal(resumed.budgetUsed, first.budgetUsed);
  const r2 = resumed.run();
  assert.equal(decidedOutcome(r2), 'lt');

  // an uninterrupted session reaches the same decision at the same levels
  const p3 = mkProviders();
  const direct = new CompareSession(p3.left, p3.right);
  assert.equal(decidedOutcome(direct.run()), 'lt');
  assert.equal(resumed.leftLevel, direct.leftLevel);
  assert.equal(resumed.rightLevel, direct.rightLevel);
});

test('digit stream refinement: 22/7 vs pi digits', () => {
  const pi = digitStreamProvider('pi', '3.141592653589793238462643383279', 'pi-source', 2);
  const approx = exactProvider('22/7', parseRational('22/7'));
  const s = new CompareSession(approx, pi);
  const r = s.run();
  assert.equal(decidedOutcome(r), 'gt'); // 22/7 = 3.142857... > pi
});

test('digit stream vs its own truncation: pending until digits decide', () => {
  const stream = digitStreamProvider('x', '0.99999999999999999991', 'x-src', 4);
  const one = exactProvider('one', parseRational('1'));
  const s = new CompareSession(stream, one, 2);
  const early = s.run();
  assert.equal(early.kind, 'undetermined'); // 1 still inside [x, x+radius]
  const resumed = CompareSession.resume(s.suspend(), stream, one, 10);
  assert.equal(decidedOutcome(resumed.run()), 'lt'); // 0.999...91 < 1
});

test('non-refinable overlapping intervals stay undetermined (terminal pending)', () => {
  const s = new CompareSession(
    fixedIntervalProvider('a', parseRational('1'), 'A', parseRational('0.5')),
    fixedIntervalProvider('b', parseRational('1'), 'B', parseRational('0.5')),
  );
  const r = s.run();
  assert.equal(r.kind, 'undetermined');
  assert.match((r as { reason: string }).reason, /no finer evidence/);
});

test('shared provenance inside the protocol decides eq without refinement', () => {
  const s = new CompareSession(
    fixedIntervalProvider('a', parseRational('2.5'), 'shared-sensor', parseRational('0.25')),
    fixedIntervalProvider('b', parseRational('2.5'), 'shared-sensor', parseRational('0.25')),
    0,
  );
  assert.equal(decidedOutcome(s.run()), 'eq');
});

test('budget accounting is exact and monotone', () => {
  const s = new CompareSession(
    halvingProvider('L', parseRational('0'), 'L', parseRational('1')),
    halvingProvider('R', parseRational('0.1'), 'R', parseRational('1')),
    3,
  );
  s.run();
  assert.equal(s.budgetUsed, 3);
  const state = JSON.parse(s.suspend());
  assert.equal(state.budgetUsed, 3);
  assert.equal(state.budget, 3);
});
