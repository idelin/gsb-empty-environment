import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { ValidatingAdmissionEngine } from '../src/index.ts';
import {
  AdmissionRequest,
  ValidatingAdmissionPolicy,
  ValidatingAdmissionPolicyBinding,
} from '../src/types.ts';

const policy: ValidatingAdmissionPolicy = {
  metadata: { name: 'multi-check' },
  spec: {
    failurePolicy: 'Fail',
    matchConstraints: {
      resourceRules: [
        { apiGroups: [''], apiVersions: ['v1'], resources: ['pods'], operations: ['CREATE'] },
      ],
    },
    variables: [{ name: 'labels', expression: 'object.metadata.labels' }],
    validations: [
      {
        expression: 'has(object.metadata.labels) && has(object.metadata.labels.app)',
        message: 'app label is required',
      },
      {
        expression: 'has(object.metadata.labels) && has(object.metadata.labels.owner)',
        messageExpression: '"missing owner label on " + object.metadata.name',
      },
      {
        expression: 'object.spec.containers.size() > 0',
        message: 'at least one container is required',
      },
    ],
  },
};

const makeBinding = (
  name: string,
  validationActions: Array<'Deny' | 'Warn' | 'Audit'>,
): ValidatingAdmissionPolicyBinding => ({
  metadata: { name },
  spec: { policyName: 'multi-check', validationActions },
});

const badPod: AdmissionRequest = {
  operation: 'CREATE',
  resource: { apiGroup: '', apiVersion: 'v1', resource: 'pods' },
  namespace: 'default',
  name: 'bad-pod',
  object: {
    metadata: { name: 'bad-pod', labels: {} },
    spec: { containers: [] },
  },
};

describe('multi-binding action aggregation', () => {
  test('Deny + Warn + Audit from three bindings are all collected', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policy);
    engine.registerBinding(makeBinding('deny-binding', ['Deny']));
    engine.registerBinding(makeBinding('warn-binding', ['Warn']));
    engine.registerBinding(makeBinding('audit-binding', ['Audit']));

    const decision = engine.evaluate(badPod);

    // Any Deny failure rejects the whole request.
    assert.equal(decision.denied, true);
    assert.equal(decision.allowed, false);

    // Every binding evaluated independently, each collecting all three validation failures.
    assert.equal(decision.bindingEvaluations.length, 3);
    for (const evaluation of decision.bindingEvaluations) {
      assert.equal(evaluation.matched, true);
      assert.equal(evaluation.failures.length, 3);
    }
    assert.equal(decision.failures.length, 9);

    // Warnings remain available even though the request is denied.
    const warnMessages = decision.warnings.filter((line) => line.startsWith('warn-binding: '));
    assert.equal(warnMessages.length, 3);
    assert.ok(warnMessages.some((line) => line.includes('app label is required')));

    // Audit annotations remain available even though the request is denied.
    const auditKeys = Object.keys(decision.auditAnnotations);
    const auditBindingKeys = auditKeys.filter((key) =>
      key.startsWith('validation.admission.k8s.io/audit-binding:'),
    );
    assert.equal(auditBindingKeys.length, 3);
    for (const key of auditBindingKeys) {
      assert.equal(typeof decision.auditAnnotations[key], 'string');
    }

    // Deny reasons reference the deny binding only.
    assert.equal(decision.reasons.length, 3);
    assert.ok(decision.reasons.every((line) => line.startsWith('deny-binding: ')));

    // messageExpression output is rendered for the owner-label failure.
    assert.ok(
      decision.failures.some(
        (failure) => failure.message === 'missing owner label on bad-pod',
      ),
    );
  });

  test('combined Deny+Warn+Audit actions on one binding populate all three channels', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policy);
    engine.registerBinding(makeBinding('combo-binding', ['Deny', 'Warn', 'Audit']));

    const decision = engine.evaluate(badPod);
    assert.equal(decision.denied, true);
    assert.equal(decision.warnings.length, 3);
    assert.equal(Object.keys(decision.auditAnnotations).length, 3);
    assert.equal(decision.reasons.length, 3);
  });

  test('a passing object yields no failures across any binding', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policy);
    engine.registerBinding(makeBinding('deny-binding', ['Deny']));
    engine.registerBinding(makeBinding('warn-binding', ['Warn']));

    const goodPod: AdmissionRequest = {
      operation: 'CREATE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'pods' },
      namespace: 'default',
      name: 'good-pod',
      object: {
        metadata: { name: 'good-pod', labels: { app: 'web', owner: 'team' } },
        spec: { containers: [{ name: 'c', image: 'nginx' }] },
      },
    };
    const decision = engine.evaluate(goodPod);
    assert.equal(decision.allowed, true);
    assert.deepEqual(decision.warnings, []);
    assert.deepEqual(decision.auditAnnotations, {});
    assert.deepEqual(decision.reasons, []);
  });

  test('non-matching bindings do not contribute failures', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policy);
    engine.registerBinding(makeBinding('deny-binding', ['Deny']));

    const serviceRequest: AdmissionRequest = {
      operation: 'CREATE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'services' },
      object: { metadata: { name: 'svc' } },
    };
    const decision = engine.evaluate(serviceRequest);
    assert.equal(decision.allowed, true);
    assert.equal(decision.bindingEvaluations[0].matched, false);
    assert.match(
      decision.bindingEvaluations[0].skippedReason ?? '',
      /matchConstraints did not match/,
    );
  });
});

describe('independent validation evaluation (no short circuit on first failure)', () => {
  test('later Warn/Audit validations still run after an earlier Deny validation fails', () => {
    const warnOnlyPolicy: ValidatingAdmissionPolicy = {
      metadata: { name: 'separate-actions' },
      spec: {
        matchConstraints: {
          resourceRules: [
            { apiGroups: [''], apiVersions: ['v1'], resources: ['pods'], operations: ['CREATE'] },
          ],
        },
        validations: [
          { expression: 'has(object.metadata.labels.app)', message: 'deny: no app label' },
          { expression: 'has(object.metadata.labels.owner)', message: 'warn: no owner label' },
        ],
      },
    };

    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(warnOnlyPolicy);
    engine.registerBinding({
      metadata: { name: 'combo' },
      spec: { policyName: 'separate-actions', validationActions: ['Deny', 'Warn', 'Audit'] },
    });

    const decision = engine.evaluate(badPod);
    assert.equal(decision.failures.length, 2);
    const messages = decision.failures.map((failure) => failure.message);
    assert.deepEqual(messages, ['deny: no app label', 'warn: no owner label']);
    assert.equal(decision.warnings.length, 2);
    assert.equal(Object.keys(decision.auditAnnotations).length, 2);
  });
});
