// Demo: detect inputs where allow and deny rules apply simultaneously.
// Run with: npm run demo

import { ConflictAnalyzer } from '../src/index.ts';
import type { Report } from '../src/index.ts';

function printReport(title: string, report: Report): void {
  console.log(`\n=== ${title} ===`);
  console.log(`status: ${report.status}${report.pendingReason ? ` (${report.pendingReason})` : ''}`);
  if (report.regions.length === 0) {
    console.log('conflicts: none');
  } else {
    console.log('conflict regions (normalized, pairwise disjoint):');
    for (const region of report.regions) {
      const desc = Object.entries(region.description)
        .map(([attr, d]) => `${attr}=${d}`)
        .join(', ');
      console.log(`  #${region.regionId} { ${desc} }`);
      console.log(`      allow: [${region.allowRules}] deny: [${region.denyRules}]`);
      console.log(`      minimal counterexample: ${JSON.stringify(region.witness)}`);
    }
  }
  console.log(`minimal conflict rule sets: ${JSON.stringify(report.minimalConflictSets)}`);
  console.log(`rules involved: [${report.rulesInvolved}]`);
  const v = report.verification;
  console.log(
    `verification: ${v.mode}, checked ${v.checked}/${v.total} points` +
      `${v.pending ? ' (PENDING: budget too small, unvisited points undecided)' : ''}` +
      `, mismatches: ${v.mismatches.length}`,
  );
  const s = report.stats;
  console.log(`stats: dnf cache hits=${s.dnfCacheHits} misses=${s.dnfCacheMisses}, pair computations=${s.pairComputations}`);
}

// --- Policy: documents service -------------------------------------------
const analyzer = new ConflictAnalyzer({
  role: { kind: 'enum', values: ['admin', 'editor', 'viewer'] },
  tier: { kind: 'enum', values: ['free', 'pro', 'enterprise'] },
  age: { kind: 'int', min: 0, max: 120 },
});

analyzer.addRule({
  id: 'allow-staff',
  effect: 'allow',
  when: { op: 'in', attr: 'role', values: ['admin', 'editor'] },
});
analyzer.addRule({
  id: 'allow-adult-enterprise',
  effect: 'allow',
  when: { op: 'and', args: [
    { op: 'in', attr: 'tier', values: ['enterprise'] },
    { op: 'cmp', attr: 'age', cmp: '>=', value: 18 },
  ] },
});
analyzer.addRule({
  id: 'deny-minor-editor',
  effect: 'deny',
  when: { op: 'and', args: [
    { op: 'in', attr: 'role', values: ['editor'] },
    { op: 'cmp', attr: 'age', cmp: '<', value: 21 },
  ] },
});
analyzer.addRule({
  id: 'deny-free-tier',
  effect: 'deny',
  when: { op: 'in', attr: 'tier', values: ['free'] },
});

printReport('initial analysis', analyzer.analyze());

// --- Incremental: retract one deny rule, recompute ------------------------
analyzer.removeRule('deny-free-tier');
printReport('after retracting deny-free-tier (incremental)', analyzer.analyze());

// --- Budget-limited run: search cut off -> partial, never "no conflict" ---
const limited = new ConflictAnalyzer(
  { x: { kind: 'int', min: 0, max: 1000 } },
  { maxRegions: 2, maxEnum: 500 },
);
limited.addRule({ id: 'allow-all', effect: 'allow', when: { op: 'const', value: true } });
for (let i = 0; i < 5; i++) {
  limited.addRule({
    id: `deny-island-${i}`,
    effect: 'deny',
    when: { op: 'cmp', attr: 'x', cmp: '==', value: i * 200 },
  });
}
printReport('budget-limited (maxRegions=2, maxEnum=500)', limited.analyze());
