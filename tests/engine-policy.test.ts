import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { ValidatingAdmissionEngine } from '../src/index.ts';
import {
  AdmissionRequest,
  ParameterObject,
  ValidatingAdmissionPolicy,
  ValidatingAdmissionPolicyBinding,
} from '../src/types.ts';

const limitPolicy = (
  failurePolicy: 'Fail' | 'Ignore' = 'Fail',
): ValidatingAdmissionPolicy => ({
  apiVersion: 'admissionregistration.k8s.io/v1',
  kind: 'ValidatingAdmissionPolicy',
  metadata: { name: 'replica-limit' },
  spec: {
    failurePolicy,
    paramKind: { apiVersion: 'policy.example.com/v1', kind: 'ReplicaLimit' },
    matchConstraints: {
      resourceRules: [
        {
          apiGroups: ['apps'],
          apiVersions: ['v1'],
          resources: ['deployments'],
          operations: ['CREATE', 'UPDATE'],
        },
      ],
    },
    validations: [
      {
        expression: 'object.spec.replicas <= params.maxReplicas',
        message: 'replicas exceed the configured limit',
      },
    ],
  },
});

const limitParam = (namespace: string, name: string, maxReplicas: number): ParameterObject => ({
  apiVersion: 'policy.example.com/v1',
  kind: 'ReplicaLimit',
  metadata: {
    namespace,
    name,
    labels: { tier: name === 'gold' ? 'gold' : 'silver' },
  },
  maxReplicas,
});

const deploymentRequest = (replicas: number): AdmissionRequest => ({
  operation: 'CREATE',
  resource: { apiGroup: 'apps', apiVersion: 'v1', resource: 'deployments' },
  namespace: 'team-a',
  name: 'web',
  object: { spec: { replicas } },
});

const binding = (
  name: string,
  paramRef: ValidatingAdmissionPolicyBinding['spec']['paramRef'],
  validationActions: Array<'Deny' | 'Warn' | 'Audit'> = ['Deny'],
): ValidatingAdmissionPolicyBinding => ({
  apiVersion: 'admissionregistration.k8s.io/v1',
  kind: 'ValidatingAdmissionPolicyBinding',
  metadata: { name },
  spec: {
    policyName: 'replica-limit',
    paramRef,
    validationActions,
  },
});

describe('parameter expansion: one independent evaluation per parameter object', () => {
  test('a selector matching K params produces K independent validation outcomes', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(limitPolicy());
    engine.addParameter(limitParam('team-a', 'gold', 5));
    engine.addParameter(limitParam('team-a', 'silver', 2));
    engine.registerBinding(
      binding('limit-binding', {
        namespace: 'team-a',
        selector: { matchExpressions: [{ key: 'tier', operator: 'Exists' }] },
      }),
    );

    const decision = engine.evaluate(deploymentRequest(4));
    assert.equal(decision.denied, true);
    const evaluation = decision.bindingEvaluations[0];
    assert.deepEqual(evaluation.parameterKeys, ['team-a/gold', 'team-a/silver']);
    // 4 <= 5 passes for gold; 4 <= 2 fails for silver.
    assert.equal(evaluation.failures.length, 1);
    assert.equal(evaluation.failures[0].parameter, 'team-a/silver');

    const allowed = engine.evaluate(deploymentRequest(1));
    assert.equal(allowed.allowed, true);
    assert.equal(allowed.failures.length, 0);

    const bothFail = engine.evaluate(deploymentRequest(9));
    assert.equal(bothFail.failures.length, 2);
    assert.deepEqual(
      bothFail.failures.map((entry) => entry.parameter).sort(),
      ['team-a/gold', 'team-a/silver'],
    );
  });
});

describe('parameterNotFoundAction', () => {
  test('Deny (default) fails the binding when the parameter is absent', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(limitPolicy());
    engine.registerBinding(binding('b', { name: 'missing', namespace: 'team-a' }));

    const decision = engine.evaluate(deploymentRequest(1));
    assert.equal(decision.denied, true);
    assert.equal(decision.bindingEvaluations[0].errored, true);
    assert.match(decision.reasons[0], /parameter not found/);
  });

  test('Allow skips the binding silently when the parameter is absent', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(limitPolicy());
    engine.registerBinding(
      binding('b', {
        name: 'missing',
        namespace: 'team-a',
        parameterNotFoundAction: 'Allow',
      }),
    );

    const decision = engine.evaluate(deploymentRequest(100));
    assert.equal(decision.allowed, true);
    assert.equal(decision.failures.length, 0);
    assert.equal(decision.bindingEvaluations[0].matched, true);
    assert.match(
      decision.bindingEvaluations[0].skippedReason ?? '',
      /parameterNotFoundAction=Allow/,
    );
  });

  test('a selector that matches nothing also honors the not-found action', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(limitPolicy());
    engine.registerBinding(
      binding('b', {
        namespace: 'team-a',
        selector: { matchLabels: { tier: 'platinum' } },
        parameterNotFoundAction: 'Deny',
      }),
    );
    const decision = engine.evaluate(deploymentRequest(1));
    assert.equal(decision.denied, true);
  });
});

describe('matchConditions and failurePolicy', () => {
  const policyWithCondition = (failurePolicy: 'Fail' | 'Ignore'): ValidatingAdmissionPolicy => ({
    metadata: { name: 'conditional' },
    spec: {
      failurePolicy,
      matchConstraints: {
        resourceRules: [
          { apiGroups: [''], apiVersions: ['v1'], resources: ['configmaps'], operations: ['*'] },
        ],
      },
      matchConditions: [
        // Direct dereference throws a runtime error when object.data is absent.
        { name: 'has-data', expression: 'size(object.data) > 0' },
        // Plain boolean gate.
        { name: 'not-system', expression: 'object.metadata.name != "system"' },
      ],
      validations: [
        { expression: 'object.data.key == "allowed"', message: 'key must be allowed' },
      ],
    },
  });

  const cmRequest = (data?: Record<string, string>, name = 'cm'): AdmissionRequest => ({
    operation: 'CREATE',
    resource: { apiGroup: '', apiVersion: 'v1', resource: 'configmaps' },
    object: data === undefined ? { metadata: { name } } : { metadata: { name }, data },
  });

  test('a false matchCondition short-circuits: validations never run', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policyWithCondition('Fail'));
    engine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'conditional', validationActions: ['Deny'] },
    });

    const decision = engine.evaluate(cmRequest({ key: 'forbidden' }, 'system'));
    assert.equal(decision.allowed, true);
    assert.equal(decision.bindingEvaluations[0].matched, true);
    assert.equal(decision.bindingEvaluations[0].failures.length, 0);
  });

  test('runtime error in a matchCondition denies under failurePolicy=Fail', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policyWithCondition('Fail'));
    engine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'conditional', validationActions: ['Deny'] },
    });

    const decision = engine.evaluate(cmRequest(undefined));
    assert.equal(decision.denied, true);
    assert.equal(decision.bindingEvaluations[0].errored, true);
    assert.match(decision.reasons[0], /matchCondition 'has-data' evaluation error/);
  });

  test('runtime error in a matchCondition is skipped under failurePolicy=Ignore', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy(policyWithCondition('Ignore'));
    engine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'conditional', validationActions: ['Deny'] },
    });

    const decision = engine.evaluate(cmRequest(undefined));
    assert.equal(decision.allowed, true);
    assert.equal(decision.failures.length, 0);
  });

  test('validation runtime error forks on failurePolicy: Fail denies, Ignore passes', () => {
    const policy = (failurePolicy: 'Fail' | 'Ignore'): ValidatingAdmissionPolicy => ({
      metadata: { name: 'div-policy' },
      spec: {
        failurePolicy,
        matchConstraints: {
          resourceRules: [
            { apiGroups: [''], apiVersions: ['v1'], resources: ['configmaps'], operations: ['*'] },
          ],
        },
        validations: [
          // Division by zero whenever divisor is 0.
          { expression: '1 / int(object.data.divisor) == 1', message: 'never' },
        ],
      },
    });

    const request: AdmissionRequest = {
      operation: 'CREATE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'configmaps' },
      object: { metadata: { name: 'cm' }, data: { divisor: '0' } },
    };

    const failEngine = new ValidatingAdmissionEngine();
    failEngine.registerPolicy(policy('Fail'));
    failEngine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'div-policy', validationActions: ['Deny'] },
    });
    const failDecision = failEngine.evaluate(request);
    assert.equal(failDecision.denied, true);
    assert.match(failDecision.reasons[0], /evaluation error: division by zero/);

    const ignoreEngine = new ValidatingAdmissionEngine();
    ignoreEngine.registerPolicy(policy('Ignore'));
    ignoreEngine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'div-policy', validationActions: ['Deny'] },
    });
    const ignoreDecision = ignoreEngine.evaluate(request);
    assert.equal(ignoreDecision.allowed, true);
  });
});
