import test from 'node:test';
import assert from 'node:assert/strict';
import { ModRing } from '../src/ring.ts';
import { detectEquivocation, formsAreEquivalent, type CommitmentEvidence } from '../src/commitments.ts';

test('zero-divisor coefficient representations can be equivalent on admissible inputs', () => {
  const ring = new ModRing(4);
  const admissibleInputMap = [
    [1, 0],
    [0, 2],
  ];
  const first: CommitmentEvidence = {
    commitmentId: 'c',
    source: 'commitment',
    sourceId: '1',
    sender: 'helper',
    generation: 1,
    coefficients: [1, 1],
    admissibleInputMap,
  };
  const second: CommitmentEvidence = {
    ...first,
    sourceId: '2',
    coefficients: [1, 3],
  };

  assert.equal(formsAreEquivalent(first, second, ring), true);
  assert.deepEqual(detectEquivocation([first, second], ring), []);

  const contradictory: CommitmentEvidence = {
    ...second,
    coefficients: [2, 1],
  };
  assert.equal(detectEquivocation([first, contradictory], ring).length, 1);
});
