/**
 * Runnable sample: node examples/run-sample.ts   (Node >= 23.6)
 */
import {
  repairAllocation,
  solveApportion,
  verifyConservation,
  verifyLowerBoundCertificate,
  normalizeProblem,
} from '../src/index.ts';

const show = (value: unknown): string =>
  JSON.stringify(value, (_, v) => (typeof v === 'bigint' ? `${v}` : v), 2);

function section(title: string): void {
  console.log(`\n=== ${title} ===`);
}

// 1. Council seats: exact decimal weights, largest-remainder baseline.
section('1. Ten seats, weights 0.5 / 0.3 / 0.2 (exact decimals)');
{
  const problem = {
    total: 10n,
    items: [
      { id: 'alpha', weight: '0.5' },
      { id: 'beta', weight: '0.3' },
      { id: 'gamma', weight: '0.2' },
    ],
  } as const;
  const r = solveApportion(problem);
  if (r.status === 'ok') {
    console.log('allocation:', show(r.allocation));
    console.log('baseline (largest remainder):', show(r.baseline));
    console.log('deviation:', `${r.deviation}/${r.deviationScale}`);
    console.log('conservation certificate:', show(r.certificates.conservation));
    console.log('lower-bound certificate:', show(r.certificates.lowerBound));
    console.log('enumeration proof:', show(r.certificates.enumeration));
  }
}

// 2. One indivisible unit over three equal claims.
section('2. Total 1 split three ways: exactly one object gets it');
{
  const r = solveApportion({
    total: 1n,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  });
  if (r.status === 'ok') {
    console.log('allocation:', show(r.allocation), '(equal remainders -> stable id "a")');
  }
}

// 3. Overlapping group constraints solved jointly.
section('3. Overlapping group bounds, constraints first then min deviation');
{
  const problem = {
    total: 6n,
    items: [
      { id: 'a', weight: 1n },
      { id: 'b', weight: 1n },
      { id: 'c', weight: 1n },
    ],
    groups: [
      { id: 'region-north', members: ['a', 'b'], min: 5n },
      { id: 'region-south', members: ['b', 'c'], max: 3n },
    ],
  } as const;
  const r = solveApportion(problem);
  if (r.status === 'ok') {
    console.log('allocation:', show(r.allocation));
    console.log('deviation:', `${r.deviation}/${r.deviationScale}`, '(baseline would violate region-north)');
    console.log('enumeration proof of optimality:', r.certificates.enumeration?.proven);
  }
}

// 4. Infeasible bounds: deficit reported, nothing force-fit.
section('4. Infeasible upper bounds: deficit reported, not forced');
{
  const r = solveApportion({
    total: 5n,
    items: [
      { id: 'a', weight: 1n, upper: 1n },
      { id: 'b', weight: 1n, upper: 1n },
      { id: 'c', weight: 1n, upper: 1n },
    ],
  });
  console.log('status:', r.status);
  if (r.status === 'infeasible') {
    console.log('deficit:', `${r.deficit}`, 'details:', show(r.details));
  }
}

// 5. Weight correction with a move budget.
section('5. Weight correction: minimal moves, budget, pending');
{
  const problem = {
    total: 10n,
    items: [
      { id: 'a', weight: 9n },
      { id: 'b', weight: 1n },
    ],
  } as const;
  const full = repairAllocation(problem, { a: 5n, b: 5n });
  console.log('no budget cap  ->', show(full.allocation), `moves=${full.moves}`);
  const capped = repairAllocation(problem, { a: 5n, b: 5n }, { moveBudget: 4n });
  console.log('budget 4       ->', show(capped.allocation), `moves=${capped.moves}`);
  const grown = repairAllocation({ ...problem, total: 14n }, { a: 5n, b: 5n }, { moveBudget: 2n });
  console.log('total 14, budget 2 ->', show(grown.allocation), `pending=${grown.pending} status=${grown.status}`);
  console.log('conservation certificate:', show(grown.certificates.conservation));
}

// 6. Huge exact totals: BigInt end to end, never Number.
section('6. 10^27 total with exact decimal weights');
{
  const r = solveApportion({
    total: 10n ** 27n,
    items: [
      { id: 'x', weight: '0.1' },
      { id: 'y', weight: '0.2' },
      { id: 'z', weight: '0.7' },
    ],
  });
  if (r.status === 'ok') {
    console.log('allocation:', show(r.allocation));
    console.log('deviation:', r.deviation.toString());
  }
}

// 7. Independent certificate verification (as a third party would do).
section('7. Third-party verification of certificates');
{
  const problem = {
    total: 10n,
    items: [
      { id: 'a', weight: '1/3' },
      { id: 'b', weight: '1/3' },
      { id: 'c', weight: '1/3' },
    ],
  } as const;
  const r = solveApportion(problem);
  if (r.status === 'ok') {
    const norm = normalizeProblem(problem);
    console.log('conservation:', show(verifyConservation(norm, r.allocation)));
    console.log('lower bound :', show(verifyLowerBoundCertificate(norm, r.certificates.lowerBound)));
    console.log(`optimal by relaxation bound: ${r.certificates.lowerBound.optimal}`);
  }
}
