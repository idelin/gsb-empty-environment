import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  parseRational,
  parseValue,
  exactValue,
  compareAffine,
  UnsupportedLiteralError,
  type Outcome,
} from '../src/index.ts';

const REFERENCE: string[] = [
  '-2',
  '-1/3',
  '0',
  '0.000000000000000000000000001', // 1e-27, tiny but exact
  '0.1',
  '1/3',
  '0.333333333333333333333333333333333', // 33 threes, still below 1/3
  '0.5',
  '2/3',
  '1',
  '1180591620717411303424', // 2^70
  '1180591620717411303425', // 2^70 + 1
  '1e30',
];

function outcomeOf(x: string, y: string): Outcome {
  const r = compareAffine(exactValue(parseRational(x)), exactValue(parseRational(y)));
  assert.equal(r.kind, 'decided', `exact comparison ${x} vs ${y} must decide`);
  return (r as { outcome: Outcome }).outcome;
}

test('finite exact reference set: total order, antisymmetry, transitivity', () => {
  const table = new Map<string, Outcome>();
  for (const a of REFERENCE) {
    for (const b of REFERENCE) {
      const ab = outcomeOf(a, b);
      table.set(`${a}|${b}`, ab);
      // reflexivity
      if (a === b) assert.equal(ab, 'eq');
      // antisymmetry
      const ba = outcomeOf(b, a);
      const flip: Record<Outcome, Outcome> = { lt: 'gt', gt: 'lt', eq: 'eq' };
      assert.equal(ba, flip[ab], `antisymmetry violated for ${a} vs ${b}`);
    }
  }
  // transitivity over all triples of the finite reference set
  const rank = (o: Outcome): number => (o === 'lt' ? -1 : o === 'gt' ? 1 : 0);
  for (const a of REFERENCE) {
    for (const b of REFERENCE) {
      for (const c of REFERENCE) {
        const ab = rank(table.get(`${a}|${b}`)!);
        const bc = rank(table.get(`${b}|${c}`)!);
        const ac = rank(table.get(`${a}|${c}`)!);
        if (ab <= 0 && bc <= 0) assert.ok(ac <= 0, `transitivity: ${a}<=${b}<=${c} but ${a}>${c}`);
        if (ab >= 0 && bc >= 0) assert.ok(ac >= 0, `transitivity: ${a}>=${b}>=${c} but ${a}<${c}`);
        if (ab === 0 && bc === 0) assert.equal(ac, 0);
      }
    }
  }
});

test('no epsilon equality: neighbours are never merged', () => {
  assert.equal(outcomeOf('1', '1.000000000000000000000000000001'), 'lt');
  assert.equal(outcomeOf('0.1', '0.10000000000000000000000000000001'), 'lt');
  assert.equal(outcomeOf('1/3', '0.333333333333333333333333333333333'), 'gt');
});

test('NaN and Infinity literals are rejected as unsupported', () => {
  for (const bad of ['NaN', 'nan', '-NaN', 'Infinity', '-Infinity', 'inf']) {
    assert.throws(() => parseRational(bad), UnsupportedLiteralError);
    assert.throws(() => parseValue(bad), UnsupportedLiteralError);
    assert.throws(() => parseValue(`1±${bad}`), UnsupportedLiteralError);
  }
});

test('malformed literals are rejected', () => {
  for (const bad of ['', 'abc', '1.2.3', '1/0', '0x10', '--1', '1,5']) {
    assert.throws(() => parseRational(bad));
  }
});

test('exact equality evidence is required for eq', () => {
  // same centre, independent error terms: NOT equal, undetermined
  const a = parseValue('1.5±0.1', 'source-A');
  const b = parseValue('1.5±0.1', 'source-B');
  assert.equal(compareAffine(a, b).kind, 'undetermined');
  // exact same number: eq
  const r = compareAffine(exactValue(parseRational('1.5')), exactValue(parseRational('3/2')));
  assert.deepEqual(r, { kind: 'decided', outcome: 'eq' });
});
