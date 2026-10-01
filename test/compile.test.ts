import test from 'node:test';
import assert from 'node:assert/strict';
import { make, parse, toString } from '../src/rational.ts';
import { evaluate } from '../src/evaluate.ts';
import { compile, evalCompiled } from '../src/compile.ts';
import type { Bracket, CompositeSpec } from '../src/rules.ts';

const B = (
  l: string | null,
  lc: boolean,
  u: string | null,
  uc: boolean,
  rate: string,
  fixed = '0',
  extra: Partial<Bracket> = {},
): Bracket => ({
  lower: { value: l === null ? null : parse(l), closed: lc },
  upper: { value: u === null ? null : parse(u), closed: uc },
  rate: parse(rate),
  fixed: parse(fixed),
  ...extra,
});

/** Composite exercising every feature at once. */
function richSpec(): CompositeSpec {
  return {
    rules: [
      {
        name: '佣金',
        mode: 'progressive',
        segmentRounding: { quantum: parse('0.01'), mode: 'half-up' },
        discount: parse('9/10'),
        brackets: [
          B(null, false, '0', false, '0'),
          B('0', true, '10', false, '1/10'),
          B('10', true, null, false, '1/5', '0', { max: parse('1.5') }),
        ],
      },
      {
        name: '服务费',
        mode: 'whole',
        brackets: [
          B(null, false, '0', false, '0'),
          B('0', true, '20', false, '0', '5'),
          B('20', true, null, false, '1/100', '3'),
        ],
      },
    ],
    min: parse('0'),
    max: parse('50'),
    finalRounding: { quantum: parse('0.01'), mode: 'half-even' },
  };
}

test('compiled piecewise equals direct evaluation on a rational grid (breakpoints, negatives)', () => {
  const spec = richSpec();
  const c = compile(spec);
  for (let k = -80; k <= 240; k++) {
    const x = make(BigInt(k), 4n);
    const direct = evaluate(spec, x).output;
    const viaPieces = evalCompiled(c, x);
    assert.equal(toString(viaPieces), toString(direct), `x = ${toString(x)}`);
  }
});

test('compiled evaluation is exact at every breakpoint', () => {
  const spec = richSpec();
  const c = compile(spec);
  assert.deepEqual(c.breakpoints.map(toString), ['0', '10', '20']);
  for (const bp of c.breakpoints) {
    assert.equal(toString(evalCompiled(c, bp)), toString(evaluate(spec, bp).output));
  }
});

test('whole-mode bracket ownership at breakpoints matches direct evaluation', () => {
  const spec: CompositeSpec = {
    rules: [
      {
        name: 'w',
        mode: 'whole',
        brackets: [B(null, false, '0', true, '1'), B('0', false, '5', true, '2'), B('5', false, null, false, '3')],
      },
    ],
  };
  const c = compile(spec);
  for (const x of ['-1', '0', '1/2', '5', '6']) {
    assert.equal(toString(evalCompiled(c, parse(x))), toString(evaluate(spec, parse(x)).output), `x=${x}`);
  }
  assert.equal(toString(evalCompiled(c, parse('0'))), '0'); // closed lower bracket owns 0
  assert.equal(toString(evalCompiled(c, parse('5'))), '10'); // closed middle bracket owns 5
});
