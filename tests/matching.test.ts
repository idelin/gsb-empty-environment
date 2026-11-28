import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { labelSelectorMatches, matchResources, ruleMatchesResource } from '../src/matching.ts';
import { AdmissionRequest, MatchResources, NamedRuleWithOperations } from '../src/types.ts';

const podsRule: NamedRuleWithOperations = {
  apiGroups: [''],
  apiVersions: ['v1'],
  resources: ['pods'],
  operations: ['CREATE', 'UPDATE'],
};

const podsStarRule: NamedRuleWithOperations = {
  apiGroups: [''],
  apiVersions: ['v1'],
  resources: ['pods/*'],
  operations: ['CREATE', 'UPDATE'],
};

describe('subresource matching semantics', () => {
  test('resource "pods" matches the parent resource only', () => {
    assert.equal(
      ruleMatchesResource(podsRule, { apiGroup: '', apiVersion: 'v1', resource: 'pods' }, 'CREATE'),
      true,
    );
    assert.equal(
      ruleMatchesResource(
        podsRule,
        { apiGroup: '', apiVersion: 'v1', resource: 'pods', subresource: 'status' },
        'UPDATE',
      ),
      false,
    );
  });

  test('resource "pods/*" matches subresources but never the parent resource', () => {
    assert.equal(
      ruleMatchesResource(
        podsStarRule,
        { apiGroup: '', apiVersion: 'v1', resource: 'pods', subresource: 'status' },
        'UPDATE',
      ),
      true,
    );
    assert.equal(
      ruleMatchesResource(
        podsStarRule,
        { apiGroup: '', apiVersion: 'v1', resource: 'pods', subresource: 'eviction' },
        'CREATE',
      ),
      true,
    );
    assert.equal(
      ruleMatchesResource(podsStarRule, { apiGroup: '', apiVersion: 'v1', resource: 'pods' }, 'CREATE'),
      false,
    );
  });

  test('wildcard "*" matches both parent resources and subresources', () => {
    const starRule: NamedRuleWithOperations = {
      apiGroups: ['*'],
      apiVersions: ['*'],
      resources: ['*'],
      operations: ['*'],
    };
    assert.equal(
      ruleMatchesResource(starRule, { apiGroup: 'apps', apiVersion: 'v1', resource: 'deployments' }, 'UPDATE'),
      true,
    );
    assert.equal(
      ruleMatchesResource(
        starRule,
        { apiGroup: '', apiVersion: 'v1', resource: 'pods', subresource: 'status' },
        'UPDATE',
      ),
      true,
    );
  });

  test('explicitly listing both "pods" and "pods/*" covers parent and subresources', () => {
    const both: NamedRuleWithOperations = {
      apiGroups: [''],
      apiVersions: ['v1'],
      resources: ['pods', 'pods/*'],
      operations: ['*'],
    };
    assert.equal(
      ruleMatchesResource(both, { apiGroup: '', apiVersion: 'v1', resource: 'pods' }, 'CREATE'),
      true,
    );
    assert.equal(
      ruleMatchesResource(
        both,
        { apiGroup: '', apiVersion: 'v1', resource: 'pods', subresource: 'status' },
        'UPDATE',
      ),
      true,
    );
  });

  test('operation mismatch vetoes a rule', () => {
    assert.equal(
      ruleMatchesResource(podsRule, { apiGroup: '', apiVersion: 'v1', resource: 'pods' }, 'DELETE'),
      false,
    );
  });
});

describe('excludeResourceRules precedence', () => {
  const constraints: MatchResources = {
    resourceRules: [
      { apiGroups: [''], apiVersions: ['v1'], resources: ['pods'], operations: ['*'] },
    ],
    excludeResourceRules: [
      {
        apiGroups: [''],
        apiVersions: ['v1'],
        resources: ['pods/*'],
        operations: ['*'],
      },
    ],
  };

  const baseRequest = (subresource?: string): AdmissionRequest => ({
    operation: 'UPDATE',
    resource: {
      apiGroup: '',
      apiVersion: 'v1',
      resource: 'pods',
      subresource,
    },
    object: { metadata: { name: 'p', labels: {} } },
  });

  test('inclusion still applies to the parent resource', () => {
    assert.equal(matchResources(constraints, baseRequest()), true);
  });

  test('a matching exclusion vetoes an inclusion', () => {
    assert.equal(matchResources(constraints, baseRequest('status')), false);
  });

  test('exclude rules covering everything veto all requests', () => {
    const veto: MatchResources = {
      resourceRules: [
        { apiGroups: ['*'], apiVersions: ['*'], resources: ['*'], operations: ['*'] },
      ],
      excludeResourceRules: [
        { apiGroups: ['*'], apiVersions: ['*'], resources: ['*'], operations: ['*'] },
      ],
    };
    assert.equal(matchResources(veto, baseRequest()), false);
  });
});

describe('label selectors', () => {
  const labels = { app: 'web', tier: 'frontend', env: 'prod' };

  test('matchLabels requires every key/value pair', () => {
    assert.equal(labelSelectorMatches({ matchLabels: { app: 'web' } }, labels), true);
    assert.equal(labelSelectorMatches({ matchLabels: { app: 'db' } }, labels), false);
  });

  test('In / NotIn / Exists / DoesNotExist', () => {
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'tier', operator: 'In', values: ['frontend', 'edge'] }] },
        labels,
      ),
      true,
    );
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'tier', operator: 'In', values: ['backend'] }] },
        labels,
      ),
      false,
    );
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'env', operator: 'NotIn', values: ['dev'] }] },
        labels,
      ),
      true,
    );
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'missing', operator: 'Exists' }] },
        labels,
      ),
      false,
    );
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'missing', operator: 'DoesNotExist' }] },
        labels,
      ),
      true,
    );
    assert.equal(
      labelSelectorMatches(
        { matchExpressions: [{ key: 'env', operator: 'NotIn', values: ['prod'] }] },
        labels,
      ),
      false,
    );
  });

  test('empty selector matches everything, including absent labels', () => {
    assert.equal(labelSelectorMatches({}, undefined), true);
    assert.equal(labelSelectorMatches(undefined, undefined), true);
  });

  test('namespaceSelector and objectSelector are both enforced', () => {
    const constraints: MatchResources = {
      resourceRules: [
        { apiGroups: [''], apiVersions: ['v1'], resources: ['pods'], operations: ['*'] },
      ],
      namespaceSelector: { matchLabels: { tenant: 'a' } },
      objectSelector: { matchExpressions: [{ key: 'app', operator: 'Exists' }] },
    };
    const request: AdmissionRequest = {
      operation: 'CREATE',
      resource: { apiGroup: '', apiVersion: 'v1', resource: 'pods' },
      namespace: 'team-a',
      namespaceObject: { metadata: { labels: { tenant: 'a' } } },
      object: { metadata: { name: 'p', labels: { app: 'web' } } },
    };
    assert.equal(matchResources(constraints, request), true);

    const wrongNamespace: AdmissionRequest = {
      ...request,
      namespaceObject: { metadata: { labels: { tenant: 'b' } } },
    };
    assert.equal(matchResources(constraints, wrongNamespace), false);

    const noAppLabel: AdmissionRequest = {
      ...request,
      object: { metadata: { name: 'p', labels: {} } },
    };
    assert.equal(matchResources(constraints, noAppLabel), false);
  });

  test('namespaceSelector does not constrain cluster-scoped resources', () => {
    const constraints: MatchResources = {
      resourceRules: [
        { apiGroups: ['rbac.authorization.k8s.io'], apiVersions: ['v1'], resources: ['roles'], operations: ['*'] },
      ],
      namespaceSelector: { matchLabels: { tenant: 'a' } },
    };
    const request: AdmissionRequest = {
      operation: 'CREATE',
      clusterScoped: true,
      resource: { apiGroup: 'rbac.authorization.k8s.io', apiVersion: 'v1', resource: 'roles' },
      object: { metadata: { name: 'r' } },
    };
    assert.equal(matchResources(constraints, request), true);
  });
});
