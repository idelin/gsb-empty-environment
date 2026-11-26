import test from 'node:test';
import assert from 'node:assert/strict';
import * as root from '../index.ts';

test('package root exposes the modular repair admission API', () => {
  for (const name of [
    'ModRing',
    'smithNormalForm',
    'conditionalSupportSize',
    'exhaustiveConditionalImageSize',
    'admitRepairPlan',
    'admitOrThrow',
    'ModularRepairAdmission',
    'generateDomainKeyPair',
    'signRepairStep',
    'detectEquivocation',
    'MaskGenerationLedger',
    'findMaskCancellationWitnesses',
    'createLinearForm',
  ]) {
    assert.equal(typeof (root as Record<string, unknown>)[name], 'function', name);
  }
});
