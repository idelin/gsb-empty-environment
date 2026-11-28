import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { ValidatingAdmissionEngine } from '../src/index.ts';
import { ruleMatchesResource } from '../src/matching.ts';
import type { NamedRuleWithOperations } from '../src/types.ts';

describe('resourceNames and DELETE objectSelector', () => {
  test('resourceNames constrains matching by request name', () => {
    const rule: NamedRuleWithOperations = {
      apiGroups: [''],
      apiVersions: ['v1'],
      resources: ['configmaps'],
      operations: ['*'],
      resourceNames: ['protected'],
    };
    const gvr = { apiGroup: '', apiVersion: 'v1', resource: 'configmaps' };
    assert.equal(ruleMatchesResource(rule, gvr, 'UPDATE', 'protected'), true);
    assert.equal(ruleMatchesResource(rule, gvr, 'UPDATE', 'other'), false);
  });

  test('objectSelector on DELETE falls back to oldObject labels', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy({
      metadata: { name: 'delete-guard' },
      spec: {
        matchConstraints: {
          resourceRules: [
            { apiGroups: [''], apiVersions: ['v1'], resources: ['secrets'], operations: ['DELETE'] },
          ],
          objectSelector: { matchLabels: { protected: 'true' } },
        },
        validations: [{ expression: 'false', message: 'deletion blocked' }],
      },
    });
    engine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'delete-guard', validationActions: ['Deny'] },
    });

    const protectedDelete = engine.evaluate({
      operation: 'DELETE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'secrets' },
      name: 'db-password',
      oldObject: { metadata: { name: 'db-password', labels: { protected: 'true' } } },
    });
    assert.equal(protectedDelete.denied, true);

    const normalDelete = engine.evaluate({
      operation: 'DELETE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'secrets' },
      name: '普通',
      oldObject: { metadata: { name: '普通', labels: {} } },
    });
    assert.equal(normalDelete.allowed, true);
    assert.equal(normalDelete.bindingEvaluations[0].matched, false);
  });
});

describe('messageExpression and policy variables', () => {
  test('messageExpression renders dynamic failure text and variables are reusable', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerPolicy({
      metadata: { name: 'quota' },
      spec: {
        variables: [{ name: 'replicas', expression: 'object.spec.replicas' }],
        matchConstraints: {
          resourceRules: [
            { apiGroups: ['apps'], apiVersions: ['v1'], resources: ['deployments'], operations: ['CREATE'] },
          ],
        },
        validations: [
          {
            expression: 'variables.replicas <= 3.0',
            messageExpression: '"replicas " + string(variables.replicas) + " exceed quota 3"',
          },
        ],
      },
    });
    engine.registerBinding({
      metadata: { name: 'b' },
      spec: { policyName: 'quota', validationActions: ['Deny'] },
    });

    const decision = engine.evaluate({
      operation: 'CREATE',
      resource: { apiGroup: 'apps', apiVersion: 'v1', resource: 'deployments' },
      object: { spec: { replicas: 7 } },
    });
    assert.equal(decision.denied, true);
    assert.equal(decision.failures[0].message, 'replicas 7 exceed quota 3');
  });
});

describe('binding to an unregistered policy', () => {
  test('the binding is skipped and cannot deny the request', () => {
    const engine = new ValidatingAdmissionEngine();
    engine.registerBinding({
      metadata: { name: 'orphan' },
      spec: { policyName: 'ghost', validationActions: ['Deny'] },
    });
    const decision = engine.evaluate({
      operation: 'CREATE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'pods' },
      object: { metadata: { name: 'p' } },
    });
    assert.equal(decision.allowed, true);
    assert.equal(decision.bindingEvaluations[0].matched, false);
    assert.match(decision.bindingEvaluations[0].skippedReason ?? '', /not found/);
  });
});
