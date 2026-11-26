import test from 'node:test';
import assert from 'node:assert/strict';
import { ModRing } from '../src/ring.ts';
import {
  admitRepairPlan,
  signRepairStep,
  type RawRepairStep,
  type RepairStep,
} from '../src/admission.ts';
import { generateDomainKeyPair } from '../src/signing.ts';
import {
  exhaustiveConditionalSupportSize,
  conditionalSupportSize,
} from '../src/linear.ts';

const rings = [4, 9, 8] as const;

function signedStep(
  ring: ModRing,
  keyPair: ReturnType<typeof generateDomainKeyPair>,
  step: RawRepairStep,
): RepairStep {
  return signRepairStep(ring, step, keyPair.privateKeyPem, keyPair.keyId, keyPair.publicKeyPem);
}

test('masked linear forms preserve full secret support in exhaustive assignments', () => {
  for (const modulus of rings) {
    const ring = new ModRing(modulus);
    const masked = exhaustiveConditionalSupportSize({
      variableCount: 2,
      observationForms: [[1, 1]],
      protectedForm: [1, 0],
      ring,
    });
    assert.equal(masked.formulaSize, BigInt(ring.q));
    assert.equal(masked.minSupport, ring.q);
    assert.equal(masked.maxSupport, ring.q);
    assert.equal(masked.formulaMatches, true);

    const canceled = exhaustiveConditionalSupportSize({
      variableCount: 2,
      observationForms: [
        [1, 1],
        [0, 1],
      ],
      protectedForm: [1, 0],
      ring,
    });
    assert.equal(canceled.formulaSize, 1n);
    assert.equal(canceled.minSupport, 1);
    assert.equal(canceled.maxSupport, 1);
    assert.equal(canceled.formulaMatches, true);
  }
});

test('high-order zero divisor leaks exactly the modulo-p remainder', () => {
  for (const modulus of rings) {
    const ring = new ModRing(modulus);
    const coefficient = ring.p ** (ring.e - 1);
    const formula = conditionalSupportSize([[coefficient]], [1], ring);
    const exhaustive = exhaustiveConditionalSupportSize({
      variableCount: 1,
      observationForms: [[coefficient]],
      protectedForm: [1],
      ring,
    });
    assert.equal(formula, BigInt(ring.q / ring.p));
    assert.equal(exhaustive.minSupport, ring.q / ring.p);
    assert.equal(exhaustive.maxSupport, ring.q / ring.p);
    assert.equal(exhaustive.observationGroups, ring.p);
    assert.equal(exhaustive.formulaMatches, true);
  }
});

test('valid signed masked repair plan is admitted for Z4, Z9, and Z8', () => {
  for (const modulus of rings) {
    const ring = new ModRing(modulus);
    const helperKeys = generateDomainKeyPair('helper', 'helper-key-1');
    const masks = [{ id: 'mask-a', generation: 7, owner: 'helper' }];

    const maskedShare = signedStep(ring, helperKeys, {
      id: 'helper-masked-share',
      generation: 7,
      round: 0,
      sender: 'helper',
      receiver: 'target',
      localInputIndex: 0,
      coefficients: [1, 0, 1],
      commitmentId: 'helper-opening-0',
    });
    const openingMask = signedStep(ring, helperKeys, {
      id: 'helper-mask-opening',
      generation: 7,
      round: 1,
      sender: 'helper',
      receiver: 'target',
      localInputIndex: 1,
      coefficients: [0, 0, 1],
      dependsOn: ['helper-masked-share'],
      commitmentId: 'helper-opening-1',
    });

    const result = admitRepairPlan({
      ring,
      secretCount: 2,
      masks,
      domains: ['helper', 'target'],
      shareMatrix: [
        [1, 0],
        [1, 0],
      ],
      target: 'target',
      steps: [maskedShare, openingMask],
      publicKeys: { helper: helperKeys.publicKeyPem },
      coalitions: [{ label: 'helper-collusion', domains: ['helper'] }],
      protectedForms: 'all-secret-forms',
      exhaustive: true,
      exhaustiveMaxAssignments: 10_000,
    });

    assert.deepEqual(
      result.errors.map((issue) => issue.code),
      [],
      `expected admission for modulus ${modulus}: ${JSON.stringify(result.errors, null, 2)}`,
    );
    assert.equal(result.admitted, true);
    assert.equal(result.target?.reconstructible, true);
    assert.equal(result.target?.supportSizes[0], 1n);
    assert.equal(result.target?.finalRound, 1);
    assert.equal(result.target?.roundSupport.at(-1)?.reconstructible, true);
    assert.equal(result.coalitions[0]?.leaks.length, 0);
  }
});

test('zero-sum mask cancellation that exposes a secret is rejected and exhaustively confirmed', () => {
  for (const modulus of rings) {
    const ring = new ModRing(modulus);
    const helperKeys = generateDomainKeyPair('helper');
    const steps = [
      signedStep(ring, helperKeys, {
        id: 'masked-secret',
        generation: 3,
        round: 0,
        sender: 'helper',
        receiver: 'receiver',
        localInputIndex: 0,
        coefficients: [1, 1],
      }),
      signedStep(ring, helperKeys, {
        id: 'mask-opening',
        generation: 3,
        round: 1,
        sender: 'helper',
        receiver: 'receiver',
        localInputIndex: 1,
        coefficients: [0, 1],
      }),
    ];

    const result = admitRepairPlan({
      ring,
      secretCount: 1,
      masks: [{ id: 'r', generation: 3, owner: 'helper' }],
      domains: ['receiver'],
      shareMatrix: [[0]],
      steps,
      publicKeys: { helper: helperKeys.publicKeyPem },
      coalitions: [{ domains: ['receiver'] }],
      protectedForms: [[1]],
      exhaustive: true,
    });

    assert.equal(result.admitted, false);
    assert.ok(result.errors.some((issue) => issue.code === 'protected-linear-distribution-shrinks'));
    assert.ok(result.errors.some((issue) => issue.code === 'zero-sum-mask-cancellation'));
    const cardinality = result.coalitions[0]?.forms[0];
    assert.equal(cardinality?.beforeSize, BigInt(ring.q));
    assert.equal(cardinality?.afterSize, 1n);
    const audit = result.coalitions[0]?.exhaustive as
      | Array<{ formulaMatches?: boolean }>
      | undefined;
    assert.equal(audit?.[0]?.formulaMatches, true);
  }
});

test('signed equivocating combinations to different receivers are intercepted', () => {
  for (const modulus of rings) {
    const ring = new ModRing(modulus);
    const helperKeys = generateDomainKeyPair('helper', 'helper-key');
    const aliceStep = signedStep(ring, helperKeys, {
      id: 'to-alice',
      generation: 11,
      round: 0,
      sender: 'helper',
      receiver: 'alice',
      localInputIndex: 0,
      coefficients: [1, 1],
      commitmentId: 'same-slot-commitment',
    });
    const bobStep = signedStep(ring, helperKeys, {
      id: 'to-bob',
      generation: 11,
      round: 0,
      sender: 'helper',
      receiver: 'bob',
      localInputIndex: 0,
      coefficients: [2, 1],
      commitmentId: 'same-slot-commitment',
    });

    const result = admitRepairPlan({
      ring,
      secretCount: 1,
      masks: [{ id: 'r', generation: 11 }],
      steps: [aliceStep, bobStep],
      publicKeys: { helper: helperKeys.publicKeyPem },
      protectedForms: [],
    });

    assert.equal(result.admitted, false);
    assert.equal(result.equivocations.length >= 1, true);
    assert.ok(result.errors.some((issue) => issue.code === 'equivocating-linear-commitment'));
  }
});

test('strict slot mode catches receiver-specific contradictory signed forms without commitment id', () => {
  const ring = new ModRing(4);
  const helperKeys = generateDomainKeyPair('helper');
  const first = signedStep(ring, helperKeys, {
    id: 'first',
    generation: 1,
    round: 2,
    sender: 'helper',
    receiver: 'alice',
    localInputIndex: 3,
    coefficients: [1, 0],
  });
  const second = signedStep(ring, helperKeys, {
    id: 'second',
    generation: 1,
    round: 2,
    sender: 'helper',
    receiver: 'bob',
    localInputIndex: 3,
    coefficients: [3, 0],
  });

  const result = admitRepairPlan({
    ring,
    secretCount: 1,
    masks: [{ id: 'r', generation: 1 }],
    steps: [first, second],
    publicKeys: { helper: helperKeys.publicKeyPem },
    slotConsistency: 'sender-round-input',
    protectedForms: [],
  });

  assert.equal(result.admitted, false);
  assert.ok(result.errors.some((issue) => issue.code === 'equivocating-slot-combination'));
});

test('cross-generation mask replay is rejected', () => {
  const ring = new ModRing(9);
  const helperKeys = generateDomainKeyPair('helper');
  const replayed = signedStep(ring, helperKeys, {
    id: 'future-generation-step',
    generation: 8,
    round: 0,
    sender: 'helper',
    receiver: 'target',
    localInputIndex: 0,
    coefficients: [1, 1],
  });

  const result = admitRepairPlan({
    ring,
    secretCount: 1,
    masks: [{ id: 'old-mask', generation: 7, owner: 'helper' }],
    steps: [replayed],
    publicKeys: { helper: helperKeys.publicKeyPem },
    protectedForms: [],
  });

  assert.equal(result.admitted, false);
  assert.ok(result.errors.some((issue) => issue.code === 'cross-generation-mask-replay'));
});

test('tampered signed coefficients fail signature verification', () => {
  const ring = new ModRing(8);
  const helperKeys = generateDomainKeyPair('helper');
  const valid = signedStep(ring, helperKeys, {
    id: 'tampered',
    generation: 1,
    round: 0,
    sender: 'helper',
    receiver: 'target',
    localInputIndex: 0,
    coefficients: [1, 1],
  });
  const tampered: RepairStep = {
    ...valid,
    coefficients: ring.normalize([3, 1]),
  };

  const result = admitRepairPlan({
    ring,
    secretCount: 1,
    masks: [{ id: 'r', generation: 1 }],
    steps: [tampered],
    publicKeys: { helper: helperKeys.publicKeyPem },
    protectedForms: [],
  });

  assert.equal(result.admitted, false);
  assert.ok(result.errors.some((issue) => issue.code === 'invalid-step-signature'));
});

test('jointly inconsistent signed values are rejected by modular consistency check', () => {
  const ring = new ModRing(4);
  const keys = generateDomainKeyPair('helper');
  const first = signedStep(ring, keys, {
    id: 'value-one',
    generation: 1,
    sender: 'helper',
    receiver: 'target',
    localInputIndex: 0,
    coefficients: [1, 0],
    value: 1,
  });
  const second = signedStep(ring, keys, {
    id: 'value-two',
    generation: 1,
    sender: 'helper',
    receiver: 'target',
    localInputIndex: 0,
    coefficients: [1, 0],
    value: 2,
  });

  const result = admitRepairPlan({
    ring,
    secretCount: 1,
    masks: [{ id: 'r', generation: 1 }],
    steps: [first, second],
    publicKeys: { helper: keys.publicKeyPem },
    target: 'target',
    shareRows: { target: [1] },
    protectedForms: [],
  });

  assert.equal(result.admitted, false);
  assert.ok(result.errors.some((issue) => issue.code === 'inconsistent-signed-values'));
});
