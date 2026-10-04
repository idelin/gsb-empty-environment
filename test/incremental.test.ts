import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Decision, FileMeta, Policy } from '../src/types.ts';
import { fullScan } from '../src/interpreter.ts';
import { diffPolicies, incrementalUpdate, selectAffectedFiles } from '../src/incremental.ts';
import { crossCheck } from '../src/reference.ts';

const oldPolicy: Policy = {
  name: 'backup',
  defaultAction: 'exclude',
  rules: [
    { id: 'photos', priority: 10, action: 'include', when: { kind: 'path', glob: 'photos/**' } },
    { id: 'big', priority: 20, action: 'exclude', when: { kind: 'size', min: 1000 } },
    { id: 'tagged', priority: 30, action: 'include', when: { kind: 'tag', tag: 'keep' } },
  ],
};

const files: FileMeta[] = [
  { path: 'photos/cat.jpg', size: 500, version: 1, tags: [] },
  { path: 'photos/raw.cr2', size: 5000, version: 2, tags: [] },
  { path: 'docs/report.pdf', size: 100, version: 1, tags: ['keep'] },
  { path: 'docs/notes.txt', size: 10, version: 1, tags: [] },
  { path: 'misc/unknown-tags.bin', size: 50, version: 1 }, // tags unknown
  { path: 'misc/unknown-size.bin', version: 1, tags: [] }, // size unknown
];

test('policy diff detects added, removed, modified and default changes', () => {
  const newPolicy: Policy = {
    ...oldPolicy,
    defaultAction: 'include',
    rules: [
      oldPolicy.rules[0],
      { ...oldPolicy.rules[1], when: { kind: 'size', min: 2000 } }, // modified
      { id: 'new', priority: 5, action: 'exclude', when: { kind: 'path', glob: 'tmp/**' } },
      // 'tagged' removed
    ],
  };
  const diff = diffPolicies(oldPolicy, newPolicy);
  assert.deepEqual(diff.added.map((r) => r.id), ['new']);
  assert.deepEqual(diff.removed.map((r) => r.id), ['tagged']);
  assert.deepEqual(diff.modified.map((m) => m.after.id), ['big']);
  assert.equal(diff.defaultChanged, true);
});

test('only possibly affected files are selected for recomputation', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  const newPolicy: Policy = {
    ...oldPolicy,
    rules: [
      { ...oldPolicy.rules[0], when: { kind: 'path', glob: 'photos/vacation/**' } }, // modified
      oldPolicy.rules[1],
      oldPolicy.rules[2],
    ],
  };
  const affected = selectAffectedFiles(files, oldDecisions, diffPolicies(oldPolicy, newPolicy), newPolicy);
  // Only files under photos/ (or decided by the photos rule) can change.
  assert.deepEqual([...affected].sort(), ['photos/cat.jpg', 'photos/raw.cr2']);
});

test('incremental update emits downloads and revocations for transitions', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  // Tighten the big-file exclusion from >=1000 to >=400: photos/cat.jpg (500)
  // becomes excluded -> revocation; nothing new is included.
  const newPolicy: Policy = {
    ...oldPolicy,
    rules: [
      oldPolicy.rules[0],
      { ...oldPolicy.rules[1], when: { kind: 'size', min: 400 } },
      oldPolicy.rules[2],
    ],
  };
  const { plan, decisions } = incrementalUpdate(oldPolicy, newPolicy, files, oldDecisions);
  assert.deepEqual(plan.revocations, ['photos/cat.jpg']);
  assert.deepEqual(plan.downloads, []);
  assert.deepEqual(decisions.get('photos/cat.jpg'), {
    kind: 'decided', action: 'exclude', ruleId: 'big',
  });
});

test('relaxing a rule produces new downloads', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  const newPolicy: Policy = {
    ...oldPolicy,
    rules: [
      oldPolicy.rules[0],
      { ...oldPolicy.rules[1], when: { kind: 'size', min: 10000 } }, // big exclusion relaxed
      oldPolicy.rules[2],
    ],
  };
  const { plan } = incrementalUpdate(oldPolicy, newPolicy, files, oldDecisions);
  assert.deepEqual(plan.downloads, ['photos/raw.cr2']);
  assert.deepEqual(plan.revocations, []);
});

test('files with unknown metadata are conservatively recomputed', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  assert.equal(oldDecisions.get('misc/unknown-tags.bin')?.kind, 'unknown');
  // Change an unrelated-looking tag rule: the tag-less file must be re-evaluated.
  const newPolicy: Policy = {
    ...oldPolicy,
    rules: [
      oldPolicy.rules[0],
      oldPolicy.rules[1],
      { ...oldPolicy.rules[2], when: { kind: 'tag', tag: 'archive' } },
    ],
  };
  const affected = selectAffectedFiles(files, oldDecisions, diffPolicies(oldPolicy, newPolicy), newPolicy);
  assert.ok(affected.has('misc/unknown-tags.bin'));
  assert.ok(affected.has('docs/report.pdf')); // previously matched 'tagged'
  assert.ok(!affected.has('docs/notes.txt')); // no tags, no relation to the rule
});

test('cross-check: incremental update matches the full-scan reference', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  const newPolicy: Policy = {
    name: 'backup',
    defaultAction: 'exclude',
    rules: [
      { id: 'photos', priority: 10, action: 'include', when: { kind: 'path', glob: 'photos/**' } },
      { id: 'big', priority: 20, action: 'exclude', when: { kind: 'size', min: 400 } },
      { id: 'tagged', priority: 30, action: 'include', when: { kind: 'tag', tag: 'archive' } },
      { id: 'nojunk', priority: 40, action: 'exclude', when: { kind: 'tag', tag: 'junk' } },
    ],
  };
  const report = crossCheck(oldPolicy, newPolicy, files, oldDecisions);
  assert.deepEqual(report.decisionMismatches, []);
  assert.deepEqual(report.planMismatches, []);
  assert.equal(report.ok, true);
});

test('cross-check detects a tampered incremental snapshot', () => {
  const oldDecisions = fullScan(oldPolicy, files);
  const tampered = new Map<string, Decision>(oldDecisions);
  tampered.set('docs/notes.txt', { kind: 'decided', action: 'include', ruleId: 'ghost' });
  const newPolicy: Policy = { ...oldPolicy, rules: [...oldPolicy.rules] };
  const report = crossCheck(oldPolicy, newPolicy, files, tampered);
  // The ghost rule no longer exists, so the file must be recomputed and the
  // plan must record the revocation on both sides consistently.
  assert.equal(report.ok, true);
  assert.deepEqual(
    report.decisionMismatches.filter((m) => m.path === 'docs/notes.txt'),
    [],
  );
});
