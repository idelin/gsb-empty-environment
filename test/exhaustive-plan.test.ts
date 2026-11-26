import test from 'node:test';
import assert from 'node:assert/strict';
import { ModRing } from '../src/ring.ts';
import { allCoefficientVectors, exhaustiveConditionalSupportSize } from '../src/linear.ts';

for (const modulus of [4, 9, 8]) {
  test(`full secret/mask enumeration preserves helper view over Z/${modulus}Z`, () => {
    const ring = new ModRing(modulus);
    const beforeForms = [
      [1, 0, 0],
      [0, 0, 1],
    ];
    const afterForms = [
      ...beforeForms,
      [1, 0, 1],
      [0, 0, 1],
    ];

    for (const secretForm of allCoefficientVectors(2, ring)) {
      const protectedForm = [...secretForm, 0];
      const before = exhaustiveConditionalSupportSize({
        variableCount: 3,
        observationForms: beforeForms,
        protectedForm,
        ring,
      });
      const after = exhaustiveConditionalSupportSize({
        variableCount: 3,
        observationForms: afterForms,
        protectedForm,
        ring,
      });

      assert.equal(before.formulaMatches, true);
      assert.equal(after.formulaMatches, true);
      assert.equal(after.minSupport, before.minSupport);
      assert.equal(after.maxSupport, before.maxSupport);
      assert.equal(after.formulaSize, before.formulaSize);
    }

    const target = exhaustiveConditionalSupportSize({
      variableCount: 3,
      observationForms: [
        [1, 0, 1],
        [0, 0, 1],
      ],
      protectedForm: [1, 0, 0],
      ring,
    });
    assert.equal(target.minSupport, 1);
    assert.equal(target.maxSupport, 1);
    assert.equal(target.formulaSize, 1n);
  });
}
