/**
 * CLI: node src/cli.ts <instance.json> [--budget=N] [--exhaustive] [--places=N] [--json]
 *
 * Prints the chosen tiers, per-source error contributions, the total
 * worst-case bound vs budget, feasible upper bound and proven lower bound,
 * and the solution certificate. Display decimals use round-half-away-from-
 * zero; all decisions use exact rationals.
 */
import { readFileSync } from 'node:fs';
import { loadInstance } from './model.ts';
import { solve } from './solve.ts';
import { exhaustiveSolve } from './exhaustive.ts';
import { issueCertificate, verifyCertificate } from './certificate.ts';
import { toDecimalString, toExactString } from './rational.ts';

const args = process.argv.slice(2);
const file = args.find((a) => !a.startsWith('--'));
if (!file) {
  console.error('usage: node src/cli.ts <instance.json> [--budget=N] [--exhaustive] [--places=N] [--json]');
  process.exit(2);
}
const opt = (name: string): string | undefined =>
  args.find((a) => a.startsWith(`--${name}=`))?.slice(name.length + 3);
const places = opt('places') ? Number(opt('places')) : 6;
const budget = opt('budget') ? Number(opt('budget')) : undefined;
const wantExhaustive = args.includes('--exhaustive');
const wantJson = args.includes('--json');

const inst = loadInstance(JSON.parse(readFileSync(file, 'utf8')));
const result = solve(inst, { nodeBudget: budget });
const cert = issueCertificate(inst, result);
const verification = verifyCertificate(inst, cert);

const fmt = (r: Parameters<typeof toDecimalString>[0]) =>
  `${toDecimalString(r, places)} (${toExactString(r)})`;

console.log(`instance: ${inst.name}`);
console.log(`error budget: ${fmt(inst.errorBudget)}`);
console.log(`status: ${result.status}${result.budgetExhausted ? ' (node budget exhausted — optimality NOT claimed)' : ''}`);
console.log(
  `nodes: explored=${result.nodesExplored} pruned(cost)=${result.nodesPrunedCost} ` +
    `pruned(error)=${result.nodesPrunedError} leaves=${result.leavesEvaluated}`,
);

if (result.incumbent) {
  console.log('\nselection (feasible upper bound):');
  for (const c of inst.components) {
    const tierId = result.incumbent.selection.get(c.id)!;
    const tier = c.tiers.find((t) => t.id === tierId)!;
    console.log(`  ${c.id} = ${tierId}   cost ${fmt(tier.cost)}   tier error radius ${fmt(tier.error)}`);
  }
  console.log(`total cost (upper bound): ${fmt(result.incumbent.cost)}`);

  console.log('\nerror breakdown by source (correlation-aware worst case):');
  for (const s of result.incumbent.error.perSource) {
    console.log(`  source ${s.sourceId} [${s.kind}]`);
    for (const t of s.terms) {
      console.log(
        `    ${t.componentId}=${t.tierId}: a=${toExactString(t.sensitivity)} e=${toExactString(t.tierError)} ` +
          `-> signed contribution ${toExactString(t.signedContribution)}`,
      );
    }
    console.log(`    signed sum: ${toExactString(s.signedSum)}`);
    console.log(`    worst case for source: ${toExactString(s.worstCase)}`);
    if (s.kind === 'shared') {
      console.log(
        `    (naive independent sum would be ${toExactString(s.independentSum)} — reported for contrast, NOT used as the bound)`,
      );
    }
  }
  console.log(`total worst-case error: ${fmt(result.incumbent.error.total)} <= budget: ${result.incumbent.error.feasible}`);
} else {
  console.log('\nno feasible selection found');
}

console.log(
  `\nbounds: lower (proven) = ${result.lowerBound ? fmt(result.lowerBound) : '+inf (infeasible)'}` +
    ` ; upper (feasible) = ${result.incumbent ? fmt(result.incumbent.cost) : 'none'}`,
);

if (wantExhaustive) {
  const ex = exhaustiveSolve(inst);
  console.log(
    `\nexhaustive cross-check: ${ex.totalCombinations} combinations, ${ex.feasibleCombinations} feasible; ` +
      `best cost ${ex.best ? fmt(ex.best.cost) : 'none'}`,
  );
  if (result.incumbent && ex.best) {
    console.log(
      `cross-check: B&B cost ${toExactString(result.incumbent.cost)} vs exhaustive ${toExactString(ex.best.cost)} -> ` +
        (toExactString(result.incumbent.cost) === toExactString(ex.best.cost) ? 'MATCH' : 'MISMATCH'),
    );
  }
}

console.log(`\ncertificate verification: valid=${verification.valid} stale=${verification.stale} optimality=${verification.optimalityProof}`);
for (const c of verification.checks) console.log(`  [${c.ok ? 'ok' : 'FAIL'}] ${c.name}: ${c.detail}`);

if (wantJson) {
  console.log('\ncertificate JSON:');
  console.log(JSON.stringify(cert, null, 2));
}
