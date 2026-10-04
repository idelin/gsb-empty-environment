import type { FileMeta, Policy } from '../src/types.ts';
import { checkConflicts } from '../src/policy.ts';
import { fullScan } from '../src/interpreter.ts';
import { incrementalUpdate } from '../src/incremental.ts';
import { crossCheck } from '../src/reference.ts';

const oldPolicy: Policy = {
  name: 'laptop-backup',
  defaultAction: 'exclude',
  rules: [
    { id: 'no-tmp', priority: 40, action: 'exclude', when: { kind: 'path', glob: 'tmp/**' } },
    { id: 'no-huge', priority: 30, action: 'exclude', when: { kind: 'size', min: 10_000 } },
    { id: 'keep-tagged', priority: 20, action: 'include', when: { kind: 'tag', tag: 'keep' } },
    { id: 'docs', priority: 10, action: 'include', when: { kind: 'path', glob: 'docs/**' } },
  ],
};

const files: FileMeta[] = [
  { path: 'docs/report.pdf', size: 2_000, version: 3, tags: ['keep'] },
  { path: 'docs/big-video.mp4', size: 50_000, version: 1, tags: [] },
  { path: 'tmp/scratch.bin', size: 10, version: 1, tags: [] },
  { path: 'photos/cat.jpg', size: 500, version: 2, tags: [] },
  { path: 'photos/untagged.raw', size: 800, version: 4 }, // tags not fetched yet
];

console.log('== static conflict check (old policy) ==');
for (const c of checkConflicts(oldPolicy)) console.log(`[${c.kind}] ${c.message}`);

const oldDecisions = fullScan(oldPolicy, files);
console.log('\n== per-file decisions (old policy) ==');
for (const [path, d] of oldDecisions) console.log(path, '->', JSON.stringify(d));

// Policy update: raise the size limit, add a negated-tag rule, drop tmp rule.
const newPolicy: Policy = {
  name: 'laptop-backup',
  defaultAction: 'exclude',
  rules: [
    { id: 'no-junk', priority: 40, action: 'exclude', when: { kind: 'tag', tag: 'junk' } },
    { id: 'no-huge', priority: 30, action: 'exclude', when: { kind: 'size', min: 100_000 } },
    { id: 'keep-tagged', priority: 20, action: 'include', when: { kind: 'tag', tag: 'keep' } },
    { id: 'docs', priority: 10, action: 'include', when: { kind: 'path', glob: 'docs/**' } },
    { id: 'photos', priority: 5, action: 'include', when: { kind: 'path', glob: 'photos/**' } },
  ],
};

console.log('\n== static conflict check (new policy) ==');
for (const c of checkConflicts(newPolicy)) console.log(`[${c.kind}] ${c.message}`);

const { plan } = incrementalUpdate(oldPolicy, newPolicy, files, oldDecisions);
console.log('\n== incremental update plan ==');
console.log('recomputed files:', plan.affectedFiles);
console.log('new downloads:   ', plan.downloads);
console.log('revocations:     ', plan.revocations);
console.log('became unknown:  ', plan.becameUnknown);
console.log('resolved:        ', plan.resolved);

const report = crossCheck(oldPolicy, newPolicy, files, oldDecisions);
console.log('\n== full-scan cross-check ==');
console.log('ok:', report.ok);
for (const m of report.decisionMismatches) console.log('decision mismatch:', m);
for (const m of report.planMismatches) console.log('plan mismatch:', m);
