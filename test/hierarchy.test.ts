import { test } from 'node:test';
import assert from 'node:assert/strict';

import { ResourceTree } from '../src/hierarchy.ts';
import type { ResourceNode } from '../src/types.ts';

const nodes: ResourceNode[] = [
  {
    id: 'arn:aws:s3:::company',
    tags: { owner: 'platform', tier: 'gold' },
  },
  {
    id: 'arn:aws:s3:::company/finance',
    parent: 'arn:aws:s3:::company',
    tags: { department: 'finance' },
  },
  {
    id: 'arn:aws:s3:::company/finance/report.pdf',
    parent: 'arn:aws:s3:::company/finance',
    tags: { classification: 'confidential' },
  },
];

test('chain walks root to leaf', () => {
  const tree = new ResourceTree(nodes);
  const chain = tree
    .chain('arn:aws:s3:::company/finance/report.pdf')
    .map((node) => node.id);
  assert.deepEqual(chain, [
    'arn:aws:s3:::company',
    'arn:aws:s3:::company/finance',
    'arn:aws:s3:::company/finance/report.pdf',
  ]);
});

test('effective tags inherit ancestors and closer nodes override', () => {
  const withOverride: ResourceNode[] = [
    nodes[0],
    nodes[1],
    { ...nodes[2], tags: { classification: 'confidential', tier: 'platinum' } },
  ];
  const tree = new ResourceTree(withOverride);
  assert.deepEqual(
    tree.effectiveTags('arn:aws:s3:::company/finance/report.pdf'),
    {
      owner: 'platform',
      tier: 'platinum',
      department: 'finance',
      classification: 'confidential',
    },
  );
});

test('applicablePolicies collects policies down the chain', () => {
  const tree = new ResourceTree([
    {
      id: 'root',
      policies: [{ id: 'root-policy', statements: [] }],
    },
    {
      id: 'root/child',
      parent: 'root',
      policies: [{ id: 'child-policy', statements: [] }],
    },
  ]);
  const policies = tree.applicablePolicies('root/child').map(
    (entry) => entry.policy.id,
  );
  assert.deepEqual(policies, ['root-policy', 'child-policy']);
});

test('unknown parent and cycles are rejected', () => {
  assert.throws(
    () => new ResourceTree([{ id: 'a', parent: 'missing' }]),
    /missing parent/,
  );
  assert.throws(
    () =>
      new ResourceTree([
        { id: 'a', parent: 'b' },
        { id: 'b', parent: 'a' },
      ]),
    /cycle detected/,
  );
});
