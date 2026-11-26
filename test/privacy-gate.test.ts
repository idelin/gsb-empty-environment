import assert from 'node:assert/strict';
import { test } from 'node:test';

import {
  FIXED_REJECTION,
  PrivacySession,
  Rational,
  billingForObservations,
  evaluatePrivacy,
  releasePrivacy,
  reviewAdaptiveStrategy,
  type AdaptiveScheduler,
  type ObservationInput,
  type PrivacyPolicyInput,
  type SignedWorkModule,
} from '../src/index.ts';
import { createHmacSignatureVerifier, signModuleHmac } from '../src/node-crypto.ts';

const SECRET = 'test-hmac-secret';
const verifier = createHmacSignatureVerifier(SECRET);

const policy: PrivacyPolicyInput = {
  secrets: ['s1', 's2'],
  priors: { s1: [1n, 10n], s2: [9n, 10n] },
  ratioCap: [2n, 1n],
  policyGeneration: 7n,
  equivalenceClasses: {
    classSmall: ['s1'],
    classLarge: ['s2'],
  },
};

function signModule(
  unsigned: Omit<SignedWorkModule, 'signature'> & { signature?: string },
): SignedWorkModule {
  const withEmptySignature: SignedWorkModule = { ...unsigned, signature: '' };
  return { ...withEmptySignature, signature: signModuleHmac(withEmptySignature, SECRET) };
}

function binaryModule(id: string, generation: bigint | number = 7n): SignedWorkModule {
  return signModule({
    id,
    policyGeneration: generation,
    resourceOf: {
      y1: 'yes',
      y2: 'yes',
      n: 'no',
    },
    tables: {
      s1: { y1: [1n, 3n], y2: [1n, 3n], n: [1n, 3n] },
      s2: { y1: [1n, 6n], y2: [1n, 6n], n: [2n, 3n] },
    },
  });
}

function simpleBinaryModule(id: string): SignedWorkModule {
  return signModule({
    id,
    policyGeneration: 7n,
    resourceOf: { y: 'yes', n: 'no' },
    tables: {
      s1: { y: [2n, 3n], n: [1n, 3n] },
      s2: { y: [1n, 3n], n: [2n, 3n] },
    },
  });
}

function backoffModule(): SignedWorkModule {
  return signModule({
    id: 'mod-backoff',
    policyGeneration: 7n,
    tables: {
      s1: { '': 1n },
      s2: { '': 1n },
    },
  });
}

const moduleA = binaryModule('mod-a');
const moduleB = binaryModule('mod-b');

test('BigInt rationals enforce strict boundary equality', () => {
  const boundary = new Rational(27n, 7n);
  const tinyOver = new Rational(27n * 10n ** 18n + 1n, 7n * 10n ** 18n);
  assert.equal(boundary.toString(), '27/7');
  assert.ok(tinyOver.gt(boundary));
  assert.ok(boundary.eq(new Rational(54n, 14n)));
  assert.equal(new Rational(1n, 3n).add([1n, 6n]).toString(), '1/2');
});

test('missing, invalid, or wrong-generation signatures are fixed rejects by default', () => {
  const session = new PrivacySession({ policy, signatureVerifier: verifier });
  const unsigned: SignedWorkModule = { ...moduleA, signature: '' };
  const badSignature: SignedWorkModule = { ...moduleA, signature: 'deadbeef' };
  const wrongGeneration = binaryModule('mod-wrong-gen', 8n);

  assert.equal(session.importModule(unsigned), FIXED_REJECTION);
  assert.equal(session.importModule(badSignature), FIXED_REJECTION);
  assert.equal(session.importModule(wrongGeneration), FIXED_REJECTION);
  assert.equal(session.importModule(moduleA)?.allowed, true);

  const defaultDenySession = new PrivacySession({ policy });
  assert.equal(defaultDenySession.importModule(moduleA), FIXED_REJECTION);

  const diagnosis = session.diagnoseModule(unsigned);
  assert.ok(diagnosis.issues.some((issue) => issue.code === 'MISSING_SIGNATURE'));
  assert.notEqual(session.importModule(badSignature), undefined);
});

test('marginal releases sit on R boundary while independent joint release exceeds it', () => {
  const allMarginalOutcomes = reviewAdaptiveStrategy({
    policyInput: policy,
    modules: [moduleA],
    signatureVerifier: verifier,
    strategy: (trace) =>
      trace.length === 0
        ? { moduleId: 'mod-a', eventId: 'e-a', sampleId: 'sample-a' }
        : null,
  });
  assert.equal(allMarginalOutcomes.traceCount, 2);
  assert.equal(allMarginalOutcomes.allowed, true, JSON.stringify(allMarginalOutcomes.issues));
  assert.ok(allMarginalOutcomes.maxLikelihoodRatio?.value?.eq(2n));

  const marginal = evaluatePrivacy({
    policyInput: policy,
    modules: [moduleA],
    signatureVerifier: verifier,
    observations: [{ eventId: 'e-a', sampleId: 'sample-a', moduleId: 'mod-a', candidateId: 'y1' }],
  });

  assert.equal(marginal.allowed, true, JSON.stringify(marginal.issues, null, 2));
  assert.ok(marginal.worstPosterior?.eq([9n, 11n]));
  assert.equal(marginal.worstPosteriorWitness?.secret, 's2');
  assert.ok(marginal.worstPosteriorMultiple?.eq([20n, 11n]));
  assert.equal(marginal.posteriorViolationWitness, null);
  assert.ok(marginal.maxLikelihoodRatio?.value?.eq([2n, 1n]));
  assert.equal(marginal.maxLrWitness?.ratio.equalsBoundary(new Rational(2n, 1n)), true);
  assert.equal(marginal.traces[0].sequence[0].publicValue, 'yes');

  const joint = evaluatePrivacy({
    policyInput: policy,
    modules: [moduleA, moduleB],
    signatureVerifier: verifier,
    observations: [
      { eventId: 'e-a', sampleId: 'sample-a', moduleId: 'mod-a', resourceId: 'yes' },
      { eventId: 'e-b', sampleId: 'sample-b', moduleId: 'mod-b', resourceId: 'yes' },
    ],
  });

  assert.equal(joint.allowed, false);
  assert.ok(joint.worstPosterior?.eq([9n, 13n]));
  assert.ok(joint.posteriorViolationWitness?.posterior.eq([4n, 13n]));
  assert.ok(joint.posteriorViolationWitness?.threshold.eq([3n, 10n]));
  assert.ok(joint.posteriorViolationWitness?.posterior.gt(joint.posteriorViolationWitness.threshold));
  assert.ok(joint.worstPosteriorMultiple?.eq([40n, 13n]));
  assert.ok(joint.maxLikelihoodRatio?.value?.eq([4n, 1n]));
  assert.equal(releasePrivacy({
    policyInput: policy,
    modules: [moduleA, moduleB],
    signatureVerifier: verifier,
    observations: [
      { eventId: 'e-a', sampleId: 'sample-a', moduleId: 'mod-a', resourceId: 'yes' },
      { eventId: 'e-b', sampleId: 'sample-b', moduleId: 'mod-b', resourceId: 'yes' },
    ],
  }), FIXED_REJECTION);
  assert.equal(joint.maxLrWitness?.numeratorSecret, 's1');
  assert.equal(joint.maxLrWitness?.denominatorSecret, 's2');
  assert.equal(joint.worstPosteriorWitness?.sequence.length, 2);
  assert.equal(joint.witnessSequence?.length, 2);
});

test('exhaustive adaptive event tree finds the joint-only violating branch', () => {
  const strategy: AdaptiveScheduler = (trace) => {
    if (trace.length === 0) {
      return { moduleId: 'mod-a', eventId: 'e-a', sampleId: 'sample-a' };
    }
    const first = trace[0];
    if (trace.length === 1 && first.moduleId === 'mod-a' && first.publicValue === 'yes') {
      return { moduleId: 'mod-b', eventId: 'e-b', sampleId: 'sample-b' };
    }
    if (trace.length === 1 && first.moduleId === 'mod-a') {
      return { moduleId: 'mod-backoff', eventId: 'e-empty', sampleId: 'sample-empty' };
    }
    return null;
  };

  const report = reviewAdaptiveStrategy({
    policyInput: policy,
    modules: [moduleA, moduleB, backoffModule()],
    signatureVerifier: verifier,
    strategy,
  });

  assert.equal(report.traceCount, 3);
  assert.equal(report.allowed, false);
  const violating = report.traces.find((trace) => !trace.compliant);
  assert.ok(violating);
  assert.deepEqual(
    violating?.sequence.map((step) => step.eventId),
    ['e-a', 'e-b'],
  );
  assert.ok(report.posteriorViolationWitness?.posterior.eq([4n, 13n]));

  const emptyTrace = report.traces.find((trace) =>
    trace.sequence.some((step) => step.billingKind === 'empty-backoff'),
  );
  assert.ok(emptyTrace);
  assert.ok(emptyTrace?.compliant);
  assert.equal(emptyTrace?.sequence.at(-1)?.publicValue, '∅');
});

test('dynamic merge is atomic, deduplicates shared event ids, and preserves state on rollback', () => {
  const session = new PrivacySession({ policy, signatureVerifier: verifier, modules: [moduleA, moduleB] });
  assert.equal(session.createGroup('observer-x').allowed, true);
  assert.equal(session.createGroup('observer-y').allowed, true);
  assert.equal(
    session.recordObservation('observer-x', {
      eventId: 'shared-a',
      sampleId: 'sample-a',
      observerId: 'x',
      moduleId: 'mod-a',
      resourceId: 'yes',
    }).allowed,
    true,
  );
  assert.equal(
    session.recordObservation('observer-y', {
      eventId: 'b-yes',
      sampleId: 'sample-b',
      observerId: 'y',
      moduleId: 'mod-b',
      resourceId: 'yes',
    }).allowed,
    true,
  );

  const failedAppend = session.recordObservation('observer-x', {
    eventId: 'b-yes-extra',
    sampleId: 'sample-b-extra',
    moduleId: 'mod-b',
    resourceId: 'yes',
  });
  assert.equal(failedAppend, FIXED_REJECTION);
  assert.equal(session.getGroup('observer-x')?.observations.length, 1);
  assert.deepEqual(session.getGroup('observer-x')?.billing.sampleIds, ['sample-a']);

  const before = session.snapshot();
  const denied = session.mergeGroups('observer-x', 'observer-y', { mergedId: 'joint-group' });
  const after = session.snapshot();

  assert.equal(denied, FIXED_REJECTION);
  assert.deepEqual(after, before);
  assert.equal(session.hasGroup('joint-group'), false);
  assert.equal(session.getGroup('observer-x')?.billing.sampleCount, 1);
  assert.equal(session.getGroup('observer-y')?.billing.sampleCount, 1);

  const preview = session.previewMerge('observer-x', 'observer-y');
  assert.equal(preview?.report.allowed, false);
  assert.ok(preview?.report.maxLikelihoodRatio?.value?.eq(4n));

  assert.equal(session.createGroup('observer-safe').allowed, true);
  assert.equal(
    session.recordObservation('observer-safe', {
      eventId: 'b-no',
      sampleId: 'sample-b-no',
      moduleId: 'mod-b',
      resourceId: 'no',
    }).allowed,
    true,
  );
  const allowedMerge = session.mergeGroups('observer-x', 'observer-safe', { mergedId: 'safe-joint' });
  assert.equal(allowedMerge.allowed, true);
  const safeGroup = session.getGroup('safe-joint');
  assert.equal(safeGroup?.billing.eventCount, 2);
  assert.equal(safeGroup?.billing.sampleCount, 2);
  assert.equal(session.hasGroup('observer-x'), false);
});

test('shared event id is deduplicated instead of charged twice', () => {
  const merged = evaluatePrivacy({
    policyInput: policy,
    modules: [moduleA],
    signatureVerifier: verifier,
    observations: [
      { eventId: 'same-event', observerId: 'x', moduleId: 'mod-a', candidateId: 'y1' },
      { eventId: 'same-event', observerId: 'y', moduleId: 'mod-a', resourceId: 'yes' },
    ],
  });

  assert.equal(merged.allowed, true);
  assert.equal(merged.traces[0].sequence.length, 1);
  assert.equal(merged.traces[0].sequence[0].likelihoodFactor.s1, '2/3');
  assert.deepEqual(billingForObservations([
    { eventId: 'same-event', observerId: 'x' },
    { eventId: 'same-event', observerId: 'y' },
  ]).eventIds, ['same-event']);
});

test('same-sample projections use the joint preimage, not multiplied marginals', () => {
  const modules = [simpleBinaryModule('bin-a'), simpleBinaryModule('bin-b')];
  const jointTables = [
    {
      id: 'correlated-draw',
      projections: ['projA', 'projB'],
      atoms: [
        { outputs: { projA: 'y', projB: 'y' }, probability: { s1: [1n, 3n], s2: [1n, 6n] } },
        { outputs: { projA: 'y', projB: 'n' }, probability: { s1: [1n, 3n], s2: [1n, 6n] } },
        { outputs: { projA: 'n', projB: 'y' }, probability: { s1: [1n, 3n], s2: [1n, 6n] } },
        { outputs: { projA: 'n', projB: 'n' }, probability: { s1: 0n, s2: [1n, 2n] } },
      ],
    },
  ] as const;

  const jointObservations: ObservationInput[] = [
    {
      eventId: 'projection-a',
      sampleId: 'same-draw',
      moduleId: 'bin-a',
      projection: 'projA',
      candidateId: 'y',
    },
    {
      eventId: 'projection-b',
      sampleId: 'same-draw',
      moduleId: 'bin-b',
      projection: 'projB',
      candidateId: 'y',
    },
  ];

  const joint = evaluatePrivacy({
    policyInput: policy,
    modules,
    jointTables: [...jointTables],
    signatureVerifier: verifier,
    observations: jointObservations,
  });

  assert.equal(joint.allowed, true, JSON.stringify(joint.issues));
  assert.equal(joint.traces[0].sequence.length, 1);
  assert.equal(joint.traces[0].sequence[0].billingKind, 'joint-preimage');
  assert.equal(joint.traces[0].likelihood.s1.toString(), '1/3');
  assert.equal(joint.traces[0].likelihood.s2.toString(), '1/6');
  assert.ok(joint.maxLikelihoodRatio?.value?.eq(2n));
  assert.equal(billingForObservations(jointObservations).sampleCount, 1);

  const independent = evaluatePrivacy({
    policyInput: policy,
    modules,
    signatureVerifier: verifier,
    observations: jointObservations.map((item, index) => ({
      ...item,
      sampleId: index === 0 ? 'draw-a' : 'draw-b',
    })),
  });
  assert.equal(independent.allowed, false);
  assert.ok(independent.maxLikelihoodRatio?.value?.eq(4n));
});

test('merge with an exhaustive strategy rejects before any state change', () => {
  const session = new PrivacySession({ policy, signatureVerifier: verifier, modules: [moduleA, moduleB, backoffModule()] });
  session.createGroup('left-empty');
  session.createGroup('right-empty');
  const before = session.snapshot();

  const strategy: AdaptiveScheduler = (trace) => {
    if (trace.length === 0) return { moduleId: 'mod-a', eventId: 'e-a', sampleId: 'a' };
    if (trace[0].publicValue === 'yes' && trace.length === 1) {
      return { moduleId: 'mod-b', eventId: 'e-b', sampleId: 'b' };
    }
    return null;
  };

  const result = session.mergeGroups('left-empty', 'right-empty', { strategy });
  assert.equal(result, FIXED_REJECTION);
  assert.deepEqual(session.snapshot(), before);
});

test('critical posterior and LR equal boundaries are allowed; exact over-bound rejects', () => {
  const criticalPolicy: PrivacyPolicyInput = {
    secrets: ['s1', 's2'],
    priors: { s1: [1n, 10n], s2: [9n, 10n] },
    ratioCap: [27n, 7n],
    policyGeneration: 1n,
  };

  const exactBoundaryModule = signModule({
    id: 'critical-exact',
    policyGeneration: 1n,
    resourceOf: { hit: 'hit', other: 'other' },
    tables: {
      s1: { hit: [27n, 100n], other: [73n, 100n] },
      s2: { hit: [7n, 100n], other: [93n, 100n] },
    },
  });

  const exact = evaluatePrivacy({
    policyInput: criticalPolicy,
    modules: [exactBoundaryModule],
    signatureVerifier: verifier,
    observations: [{ eventId: 'e-hit', moduleId: 'critical-exact', resourceId: 'hit' }],
  });

  assert.equal(exact.allowed, true, JSON.stringify(exact.issues));
  assert.ok(exact.worstPosterior?.eq([7n, 10n]));
  assert.ok(exact.worstPosteriorMultiple?.eq(3n));
  assert.ok(exact.worstPosteriorMultipleWitness?.posterior.eq([3n, 10n]));
  assert.ok(exact.worstPosteriorMultipleWitness?.threshold.eq([3n, 10n]));
  assert.ok(exact.maxLikelihoodRatio?.value?.eq([27n, 7n]));
  assert.equal(exact.maxLrWitness?.ratio.equalsBoundary(new Rational(27n, 7n)), true);

  const overModule = signModule({
    id: 'critical-over',
    policyGeneration: 1n,
    resourceOf: { hit: 'hit', other: 'other' },
    tables: {
      s1: { hit: [28n, 100n], other: [72n, 100n] },
      s2: { hit: [7n, 100n], other: [93n, 100n] },
    },
  });

  const over = evaluatePrivacy({
    policyInput: criticalPolicy,
    modules: [overModule],
    signatureVerifier: verifier,
    observations: [{ eventId: 'e-hit-over', moduleId: 'critical-over', resourceId: 'hit' }],
  });

  assert.equal(over.allowed, false);
  assert.ok(over.worstPosterior?.eq([9n, 13n]));
  assert.ok(over.posteriorViolationWitness?.posterior.eq([4n, 13n]));
  assert.ok(over.maxLikelihoodRatio?.value?.eq(4n));
  assert.ok(new Rational(4n).gt([27n, 7n]));
});
