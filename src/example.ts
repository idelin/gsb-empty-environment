/**
 * Sample run: a maximization LP with 3 inequality constraints.
 *
 *   max  x1 + x2
 *   s.t. x1 + 3 x2 <= 8
 *        3 x1 + x2 <= 9
 *        x1 + x2 <= 5
 *        x1, x2 >= 0
 *
 * Exact optimum: x = (19/8, 15/8), value 17/4, duals (1/4, 1/4, 0).
 */
import { Rational } from './rational.ts';
import { solve, type LPInput } from './simplex.ts';

const R = (s: string) => Rational.parse(s);

const lp: LPInput = {
  objective: [R('1'), R('1')],
  constraints: [
    { coeffs: [R('1'), R('3')], sense: '<=', rhs: R('8') },
    { coeffs: [R('3'), R('1')], sense: '<=', rhs: R('9') },
    { coeffs: [R('1'), R('1')], sense: '<=', rhs: R('5') },
  ],
};

const result = solve(lp);

console.log('Pivot history (exact rational arithmetic):');
for (const step of result.steps) {
  console.log(
    `  iter ${step.iteration} [phase ${step.phase}]: ` +
      `${step.enteringName} enters, ${step.leavingName} leaves ` +
      `(pivot = ${step.pivot}) -> basis { ${step.basisNames.join(', ')} }`,
  );
}

if (result.status !== 'optimal') {
  console.log('unexpected status:', result.status);
  process.exit(1);
}

console.log('\nStatus: optimal');
console.log(`Primal solution: x = (${result.x.map(String).join(', ')})`);
console.log(`Optimal value:   z = ${result.objective}`);
console.log(`Dual prices:     y = (${result.duals.map(String).join(', ')})`);

// Dual price proof: dual feasibility A^T y >= c and strong duality b^T y = z.
const A = lp.constraints.map((c) => c.coeffs);
const b = lp.constraints.map((c) => c.rhs);
const c = lp.objective;
console.log('\nDual proof:');
let dualOk = true;
for (let j = 0; j < c.length; j++) {
  let lhs = Rational.zero();
  for (let i = 0; i < A.length; i++) lhs = lhs.add(A[i][j].mul(result.duals[i]));
  const ok = lhs.cmp(c[j]) >= 0;
  dualOk = dualOk && ok;
  console.log(`  A^T y [x${j + 1}] = ${lhs} >= ${c[j]}  (${ok ? 'ok' : 'FAIL'})`);
}
let dualObj = Rational.zero();
for (let i = 0; i < b.length; i++) dualObj = dualObj.add(b[i].mul(result.duals[i]));
console.log(`  b^T y = ${dualObj} = z = ${result.objective}  (${dualObj.eq(result.objective) ? 'strong duality ok' : 'FAIL'})`);
console.log(dualOk && dualObj.eq(result.objective) ? '\nAll checks passed.' : '\nCHECK FAILED');
