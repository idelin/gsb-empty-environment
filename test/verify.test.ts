import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  metricRef,
  missingField,
  obs,
  present,
  retractedField,
  type Observation,
} from '../src/measure.ts';
import {
  applyCorrections,
  verifyCorrections,
  verifyHierarchicalMerging,
  verifySliceMerging,
} from '../src/verify.ts';
import { evaluate, ratio, source, sum } from '../src/graph.ts';
import { aggregate, mergeAggregates } from '../src/measure.ts';

const metric = metricRef('conversion-rate', 'v2');

/** Deterministic pseudo-random record generator (no Number math on values). */
function makeRecords(count: number): Observation[] {
  const records: Observation[] = [];
  let state = 123456789n;
  const next = (): bigint => {
    state = (state * 6364136223846793005n + 1442695040888963407n) % 2n ** 64n;
    return state < 0n ? -state : state;
  };
  for (let i = 0; i < count; i += 1) {
    const id = `rec-${i}`;
    const pick = Number(next() % 10n); // only used to pick a branch, never a value
    const numerator = (next() % 1000n) + 1n;
    const denominator = (next() % 100000n) + 1n;
    if (pick === 0) {
      records.push({ id, metric, numerator: missingField(), denominator: present(denominator) });
    } else if (pick === 1) {
      records.push({ id, metric, numerator: present(numerator), denominator: missingField() });
    } else if (pick === 2) {
      records.push({ id, metric, numerator: present(numerator), denominator: retractedField() });
    } else {
      records.push(obs(id, metric, numerator, denominator));
    }
  }
  return records;
}

function partition<T>(items: readonly T[], sizes: readonly number[]): T[][] {
  const out: T[][] = [];
  let offset = 0;
  for (const size of sizes) {
    out.push(items.slice(offset, offset + size));
    offset += size;
  }
  return out;
}

test('arbitrary slice merges equal the direct raw-record aggregation', () => {
  const records = makeRecords(97);
  for (const sizes of [[1], [1, 96], [31, 17, 49], [2, 3, 5, 7, 11, 13, 56], [97]]) {
    const result = verifySliceMerging(partition(records, sizes));
    assert.ok(result.ok, `sizes ${sizes}: ${result.diffs.join('; ')}`);
  }
});

test('multi-level hierarchical merges equal the direct reference', () => {
  const records = makeRecords(60);
  const slices = partition(records, [10, 20, 30]);
  const hierarchy = [[slices[0], slices[1]], [slices[2]]];
  const result = verifyHierarchicalMerging(hierarchy);
  assert.ok(result.ok, result.diffs.join('; '));
});

test('corrections (retract / replace / remove) verify against the reference', () => {
  const records = makeRecords(40);
  const slices = partition(records, [15, 25]);
  const result = verifyCorrections(slices, [
    { type: 'retract-denominator', observationId: 'rec-3' },
    { type: 'retract-numerator', observationId: 'rec-4' },
    { type: 'replace', observation: obs('rec-5', metric, 42n, 100n) },
    { type: 'remove', observationId: 'rec-6' },
  ]);
  assert.ok(result.ok, result.diffs.join('; '));
});

test('applyCorrections retracts denominators without touching numerators', () => {
  const records = [obs('a', metric, 3n, 10n), obs('b', metric, 4n, 5n)];
  const corrected = applyCorrections(records, [
    { type: 'retract-denominator', observationId: 'a' },
  ]);
  assert.equal(corrected[0].denominator.status, 'retracted');
  assert.equal(corrected[0].numerator.status, 'present');
  assert.equal(corrected[1].denominator.status, 'present');
});

test('expression graph aggregation matches direct reference with provenance', () => {
  const records = makeRecords(30);
  const slices = partition(records, [12, 18]);
  const graph = sum('total', [
    sum('h1', [source('s1', slices[0])]),
    sum('h2', [source('s2', slices[1])]),
  ]);
  const evaluated = evaluate(ratio('r', graph));
  assert.equal(evaluated.kind, 'ratio');
  const reference = aggregate(records);
  if (evaluated.kind === 'ratio') {
    const viaGraph = evaluate(graph);
    assert.ok(viaGraph.kind === 'aggregate');
    if (viaGraph.kind === 'aggregate') {
      assert.equal(viaGraph.aggregate.numeratorSum, reference.numeratorSum);
      assert.equal(viaGraph.aggregate.denominatorSum, reference.denominatorSum);
      // provenance: counted contributions rebuild the sums exactly
      const numSum = viaGraph.prov.contributions.reduce((acc, c) => acc + c.numerator, 0n);
      const denSum = viaGraph.prov.contributions.reduce((acc, c) => acc + c.denominator, 0n);
      assert.equal(numSum, reference.numeratorSum);
      assert.equal(denSum, reference.denominatorSum);
      // provenance: missing events match the reference audit counts
      const missingNum = viaGraph.prov.missing.filter(
        (e) => e.field === 'numerator' && e.status === 'missing',
      ).length;
      assert.equal(missingNum, reference.numeratorMissing);
    }
  }
});

test('mergeAggregates composes with aggregate for any split', () => {
  const records = makeRecords(50);
  const direct = aggregate(records);
  const left = aggregate(records.slice(0, 25));
  const right = aggregate(records.slice(25));
  const merged = mergeAggregates([left, right]);
  assert.equal(merged.numeratorSum, direct.numeratorSum);
  assert.equal(merged.denominatorSum, direct.denominatorSum);
  assert.equal(merged.total, direct.total);
});
