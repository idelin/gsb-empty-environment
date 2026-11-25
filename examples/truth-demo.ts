/**
 * Runnable small-tree truth demonstration.
 * Run: node --experimental-strip-types examples/truth-demo.ts
 */
import { acceptMerge } from '../src/accept.ts';
import { publishAccepted } from '../src/accept.ts';
import { exhaustiveStateTruth } from '../src/truth/oracle.ts';
import { CANONICAL_SCENARIOS } from '../src/truth/scenarios.ts';

const stateCases = exhaustiveStateTruth(3);
console.log(`truth interpreter state combinations: ${stateCases.length} (4 states x 3 branches)`);

let accepted = 0;
let rejected = 0;
for (const scenario of CANONICAL_SCENARIOS) {
  const result = acceptMerge(scenario.input);
  const status = result.ok ? 'ACCEPT' : 'REJECT';
  if (result.ok) accepted += 1;
  else rejected += 1;

  const location = result.ok
    ? `${result.leaves.length} leaf(es), id=${result.publicationId}`
    : result.errors
        .slice(0, 3)
        .map((e) => `${e.code}@${e.location.pointer || '/'}${e.location.facet ? `#${e.location.facet}` : ''}`)
        .join(', ');

  const expected = scenario.expect.ok ? 'ACCEPT' : 'REJECT';
  const mark = status === expected ? 'ok ' : 'BAD';
  console.log(`[${mark}] ${status.padEnd(6)} ${scenario.id.padEnd(42)} ${location}`);
}
console.log(`canonical scenarios: ${accepted} accepted, ${rejected} rejected`);

let publishes = 0;
await publishAccepted(
  { publish: () => void (publishes += 1) },
  CANONICAL_SCENARIOS.find((s) => s.id === 'cross-scope-tombstone-blocked')!.input,
);
console.log(`host publishes after a cross-scope rejection: ${publishes} (expected 0)`);
