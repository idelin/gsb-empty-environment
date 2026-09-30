/**
 * End-to-end demo: error-bounded measurements vs. thresholds,
 * joint feasibility with shared error variables, certificates,
 * small-domain enumeration, and incremental evidence revocation.
 *
 * Run: npm run demo
 */
import { Rational } from '../src/rational.ts';
import { Engine } from '../src/engine.ts';
import { constraint, satisfies } from '../src/constraint.ts';
import { verifyFarkas } from '../src/joint.ts';
import { verifyWitness } from '../src/judge.ts';
import { gridPoints, enumerateVerify } from '../src/enumerate.ts';
import { measurement, boxOf } from '../src/measurement.ts';
import type { Judgment } from '../src/judge.ts';

const show = (title: string, j: Judgment): void => {
  console.log(`\n=== ${title} ===`);
  switch (j.kind) {
    case 'pass':
      console.log('verdict: PASS (definite, whole error box satisfies every constraint)');
      for (const s of j.perConstraint) {
        console.log(`  ${s.constraintId}: lhs max = ${s.max.kind === 'finite' ? s.max.v.toString() : '+inf'}`);
      }
      break;
    case 'fail':
      console.log('verdict: FAIL (joint feasible region is empty)');
      if (j.certificate.type === 'farkas') {
        const f = j.certificate.farkas;
        console.log(`  Farkas certificate: 0 ${f.strict ? '<' : '<='} ${f.constant.toString()}  (contradiction)`);
        console.log(`  combination coefficients: [${f.coefficients.map((c) => c.toString()).join(', ')}]`);
        console.log(`  sources: ${f.sources.map((s) => `${s.kind}:${s.ref}`).join(', ')}`);
        console.log(`  certificate self-check: ${verifyFarkas(f, 2) ? 'VALID' : 'INVALID'}`);
      } else {
        console.log(`  constraint ${j.certificate.verdict.constraintId} already fails everywhere`);
      }
      break;
    case 'mixed':
      console.log('verdict: MIXED (feasible for some error outcomes, violated for others)');
      console.log(`  witness (passing point): ${[...j.witness].map(([k, v]) => `${k}=${v.toString()}`).join(', ')}`);
      if (j.counterexample.type === 'corner') {
        console.log(
          `  counterexample corner for ${j.counterexample.constraintId}: ` +
            [...j.counterexample.corner].map(([k, v]) => `${k}=${v.toString()}`).join(', '),
        );
      } else {
        console.log(`  counterexample: unbounded direction (${j.counterexample.constraintId})`);
      }
      break;
    case 'pending':
      console.log(`verdict: PENDING (${j.reason})`);
      break;
  }
};

// --- Scenario 1: definite pass -------------------------------------------
const engine = new Engine();
engine.setMeasurement('temp', '20', '1'); // 20 +/- 1
engine.setMeasurement('press', '5', '0.5'); // 5 +/- 0.5
engine.registerConstraintSet('safety', [
  constraint('temp-cap', { temp: '1' }, 'le', '22'),
  constraint('press-cap', { press: '1' }, 'le', '6'),
]);
show('S1 safety limits (temp<=22, press<=6)', engine.evaluate('safety'));

// --- Scenario 2: interval crosses threshold -> mixed, not "center passes" --
engine.setMeasurement('temp', '21.5', '1'); // center 21.5 <= 22, but [20.5, 22.5] crosses
const j2 = engine.evaluate('safety');
show('S2 after temp update (crosses 22)', j2);
if (j2.kind === 'mixed') {
  const caps = [
    constraint('temp-cap', { temp: '1' }, 'le', '22'),
    constraint('press-cap', { press: '1' }, 'le', '6'),
  ];
  const box = boxOf([measurement('temp', '21.5', '1'), measurement('press', '5', '0.5')]);
  console.log(`  witness verified exactly against constraints+box: ${verifyWitness(j2.witness, caps, box)}`);
  console.log(`  counterexample violates temp-cap: ${!satisfies(caps[0], (j2.counterexample as { corner: Map<string, Rational> }).corner)}`);
}

// --- Scenario 3: shared error variables -> joint infeasibility ------------
// Each constraint alone is satisfiable; together they are not.
const engine3 = new Engine();
engine3.setMeasurement('x', '0.5', '0.5'); // x in [0,1]
engine3.setMeasurement('y', '0.5', '0.5'); // y in [0,1]
engine3.registerConstraintSet('joint', [
  constraint('sum-lo', { x: '1', y: '1' }, 'le', '1'),
  constraint('sum-hi', { x: '1', y: '1' }, 'ge', '2'),
]);
show('S3 joint constraints x+y<=1 AND x+y>=2 over [0,1]^2', engine3.evaluate('joint'));

// --- Scenario 4: budget exhausted -> pending ------------------------------
const j4 = engine3.evaluate('joint', { maxCombinations: 0 });
show('S4 same problem with zero elimination budget', j4);

// --- Scenario 5: small-domain enumeration cross-check ---------------------
console.log('\n=== S5 enumeration cross-check (x,y in [0,1], 5x5 grid) ===');
const ms = [measurement('x', '0.5', '0.5'), measurement('y', '0.5', '0.5')];
const points = gridPoints(ms, 4);
const res = enumerateVerify(
  [constraint('sum-le-1', { x: '1', y: '1' }, 'le', '1'), constraint('sum-lt-2', { x: '1', y: '1' }, 'lt', '2')],
  points,
);
console.log(`grid points: ${res.totalPoints}`);
for (const [id, c] of res.perConstraint) console.log(`  ${id}: pass=${c.pass} fail=${c.fail}`);

// --- Scenario 6: exact big rationals, explicit rounding -------------------
console.log('\n=== S6 exact arithmetic / rounding ===');
const big = Rational.fromDecimal('9007199254740993.125'); // not double-representable
console.log(`parsed exactly as ${big.toString()}`);
console.log(`+ 0.875 = ${big.add(Rational.fromDecimal('0.875')).toString()}`);
const third = Rational.of(1n, 3n);
for (const mode of ['floor', 'ceil', 'trunc', 'half-up', 'half-even'] as const) {
  console.log(`  1/3 to 4 places, ${mode}: ${third.toDecimal(4, mode)}`);
}

// --- Scenario 7: history of revoked judgments ------------------------------
console.log('\n=== S7 judgment history (engine from S1/S2) ===');
for (const h of engine.history()) {
  console.log(`  #${h.serial} [${h.setId}] verdict=${h.judgment.kind} superseded: ${h.supersededBy}`);
}
