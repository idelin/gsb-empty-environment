import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { FileMeta, Policy } from '../src/types.ts';
import { fullScan } from '../src/interpreter.ts';
import { incrementalUpdate } from '../src/incremental.ts';
import { crossCheck } from '../src/reference.ts';

/**
 * Edge-case corpus: missing tags, boundary sizes, and negated conditions,
 * exercised through a policy update and verified against the full scan.
 */

const corpus: FileMeta[] = [
  { path: 'edge/exact-min.bin', size: 100, version: 5, tags: [] }, // size == min
  { path: 'edge/exact-max.bin', size: 200, version: 5, tags: [] }, // size == max
  { path: 'edge/below-min.bin', size: 99, version: 5, tags: [] },
  { path: 'edge/above-max.bin', size: 201, version: 5, tags: [] },
  { path: 'edge/no-tags.bin', size: 150, version: 5 }, // tags unknown
  { path: 'edge/no-size.bin', version: 5, tags: [] }, // size unknown
  { path: 'edge/no-version.bin', size: 150, tags: ['keep'] }, // version unknown
  { path: 'edge/junk.bin', size: 150, version: 5, tags: ['junk'] },
  { path: 'edge/keep.bin', size: 150, version: 5, tags: ['keep'] },
];

const oldPolicy: Policy = {
  name: 'edge',
  defaultAction: 'exclude',
  rules: [
    {
      id: 'band', priority: 10, action: 'include',
      when: { kind: 'all', conds: [
        { kind: 'size', min: 100, max: 200 },
        { kind: 'not', cond: { kind: 'tag', tag: 'junk' } },
      ] },
    },
    { id: 'stale', priority: 20, action: 'exclude', when: { kind: 'version', max: 3 } },
  ],
};

const newPolicy: Policy = {
  name: 'edge',
  defaultAction: 'exclude',
  rules: [
    {
      id: 'band', priority: 10, action: 'include',
      when: { kind: 'all', conds: [
        { kind: 'size', min: 99, max: 200 }, // lower bound moved by one
        { kind: 'not', cond: { kind: 'tag', tag: 'junk' } },
      ] },
    },
    { id: 'stale', priority: 20, action: 'exclude', when: { kind: 'version', max: 4 } },
    { id: 'kept', priority: 30, action: 'include', when: { kind: 'tag', tag: 'keep' } },
  ],
};

test('boundary sizes are included exactly at min and max', () => {
  const decisions = fullScan(oldPolicy, corpus);
  assert.deepEqual(decisions.get('edge/exact-min.bin'), {
    kind: 'decided', action: 'include', ruleId: 'band',
  });
  assert.deepEqual(decisions.get('edge/exact-max.bin'), {
    kind: 'decided', action: 'include', ruleId: 'band',
  });
  assert.deepEqual(decisions.get('edge/below-min.bin'), { kind: 'default', action: 'exclude' });
  assert.deepEqual(decisions.get('edge/above-max.bin'), { kind: 'default', action: 'exclude' });
});

test('missing tags make the negated-tag rule unknown, not excluded', () => {
  const decisions = fullScan(oldPolicy, corpus);
  const d = decisions.get('edge/no-tags.bin');
  assert.equal(d?.kind, 'unknown');
  if (d?.kind === 'unknown') assert.deepEqual(d.pendingRuleIds, ['band']);
});

test('negated tag excludes junk-tagged files from the band rule', () => {
  const decisions = fullScan(oldPolicy, corpus);
  assert.deepEqual(decisions.get('edge/junk.bin'), { kind: 'default', action: 'exclude' });
  assert.deepEqual(decisions.get('edge/keep.bin'), {
    kind: 'decided', action: 'include', ruleId: 'band',
  });
});

test('incremental update over the edge corpus matches the full-scan reference', () => {
  const oldDecisions = fullScan(oldPolicy, corpus);
  const report = crossCheck(oldPolicy, newPolicy, corpus, oldDecisions);
  assert.deepEqual(report.decisionMismatches, []);
  assert.deepEqual(report.planMismatches, []);
  assert.equal(report.ok, true);
});

test('moving the lower bound by one yields the expected new downloads', () => {
  const oldDecisions = fullScan(oldPolicy, corpus);
  const { plan } = incrementalUpdate(oldPolicy, newPolicy, corpus, oldDecisions);
  // edge/below-min.bin: 99 now satisfies min 99.
  // edge/no-version.bin: was 'unknown' (higher-priority 'stale' rule could not
  // be evaluated without a version); the new 'kept' rule decides it include.
  assert.deepEqual(plan.downloads, ['edge/below-min.bin', 'edge/no-version.bin']);
  assert.deepEqual(plan.revocations, []);
});
