import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  admit,
  certifyInclusion,
  compileBaseline,
  PolicyError,
  type GrammarProduction,
} from '../src/index.ts';
import { baselinePolicy, graph, node } from './fixtures.ts';

test('dangling node references are rejected with the referenced id', () => {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  const cert = certifyInclusion(old, old, 1024);
  const dangling = graph('root', [node({ id: 'root', typeName: 'List`1', args: ['missing'] })]);
  const result = admit(dangling, old, cert);
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.nodeId, 'missing');
});

test('grammars referencing undefined nonterminals are rejected at compile time', () => {
  const policy = baselinePolicy();
  const broken: GrammarProduction = {
    nt: 'Broken',
    alts: [
      {
        assembly: policy.productions[0].alts[0].assembly,
        typeName: 'Box`1',
        args: [{ nt: 'DoesNotExist', variance: 'invariant' }],
      },
    ],
  };
  policy.productions.push(broken);
  assert.throws(() => compileBaseline(policy), /dangling nonterminal ref/);
});

test('a grammar-level bare alias ring is a policy error', () => {
  const policy = baselinePolicy();
  policy.productions.push(
    { nt: 'A', alts: [], alias: 'B' } as unknown as GrammarProduction,
    { nt: 'B', alts: [], alias: 'A' } as unknown as GrammarProduction,
  );
  assert.throws(
    () => compileBaseline(policy),
    (error: unknown) => error instanceof PolicyError && /bare alias ring/.test(error.message),
  );
});

test('array rank mismatches are rejected', () => {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  old.start = 'ARR';
  const cert = certifyInclusion(old, old, 1024);
  const rankTwo = graph('r', [
    node({ id: 'r', typeName: 'Array', arrayRank: 2, element: 's' }),
    node({ id: 's', typeName: 'String' }),
  ]);
  const result = admit(rankTwo, old, cert);
  assert.equal(result.admitted, false);
  assert.match(result.rejection?.reason ?? '', /no alternative/);
});

test('a guarded cycle through an unadmitted constructor is rejected', () => {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  const cert = certifyInclusion(old, old, 1024);
  const cyclic = graph('c', [node({ id: 'c', typeName: 'UnknownList`1', args: ['c'] })]);
  const result = admit(cyclic, old, cert);
  assert.equal(result.admitted, false);
  assert.equal(result.rejection?.nodeId, 'c');
  assert.match(result.rejection?.reason ?? '', /bare alias ring|no alternative/);
});

test('the first rejection path points at the deepest offending position', () => {
  const policy = baselinePolicy();
  const old = compileBaseline(policy);
  const cert = certifyInclusion(old, old, 1024);
  const nested = graph('l1', [
    node({ id: 'l1', typeName: 'List`1', args: ['l2'] }),
    node({ id: 'l2', typeName: 'List`1', args: ['l3'] }),
    node({ id: 'l3', typeName: 'List`1', args: ['bad'] }),
    node({ id: 'bad', typeName: 'NotInGrammar' }),
  ]);
  const result = admit(nested, old, cert);
  assert.equal(result.admitted, false);
  assert.deepEqual(result.rejection?.path, [0, 0, 0]);
  assert.equal(result.rejection?.nodeId, 'bad');
});
