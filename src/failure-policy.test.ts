import { describe, test } from 'node:test';
import assert from 'node:assert/strict';

import {
  evaluateFailurePolicy,
  matchesExitCodes,
  matchesPodCondition,
  ruleMatches,
} from './failure-policy.js';
import type {
  ContainerState,
  PodFailureInfo,
  PodFailurePolicy,
  PodFailurePolicyRule,
} from './types.js';

const container = (name: string, exitCode?: number): ContainerState => ({
  name,
  exitCode,
  running: exitCode === undefined,
});

const failure = (overrides: Partial<PodFailureInfo> = {}): PodFailureInfo => ({
  index: 0,
  podName: 'pod-0',
  containers: [container('app', 42), container('sidecar', 0)],
  conditions: [],
  ...overrides,
});

describe('matchesExitCodes', () => {
  test('In matches when any inspected container exit code is in the set', () => {
    assert.equal(
      matchesExitCodes(
        { containerName: 'app', operator: 'In', values: [1, 42] },
        [container('app', 42)],
      ),
      true,
    );
  });

  test('In does not match when no exit code is in the set', () => {
    assert.equal(
      matchesExitCodes(
        { containerName: 'app', operator: 'In', values: [1, 2, 3] },
        [container('app', 42)],
      ),
      false,
    );
  });

  test('NotIn matches when every failed exit code is outside the set', () => {
    assert.equal(
      matchesExitCodes(
        { containerName: 'app', operator: 'NotIn', values: [1, 2, 3] },
        [container('app', 42)],
      ),
      true,
    );
  });

  test('NotIn fails as soon as one failed exit code belongs to the set', () => {
    assert.equal(
      matchesExitCodes(
        { operator: 'NotIn', values: [42] },
        [container('app', 42), container('worker', 7)],
      ),
      false,
    );
  });

  test('zero exit codes and running containers never match', () => {
    assert.equal(
      matchesExitCodes(
        { containerName: 'sidecar', operator: 'In', values: [0] },
        [container('sidecar', 0)],
      ),
      false,
    );
    assert.equal(
      matchesExitCodes(
        { containerName: 'app', operator: 'In', values: [1] },
        [container('app')],
      ),
      false,
    );
  });

  test('a named container that is absent does not match', () => {
    assert.equal(
      matchesExitCodes(
        { containerName: 'missing', operator: 'In', values: [42] },
        [container('app', 42)],
      ),
      false,
    );
  });
});

describe('matchesPodCondition', () => {
  const conditions = [
    { type: 'DisruptionTarget', status: 'True' as const, reason: 'EvictionByEvictionAPI' },
    { type: 'Ready', status: 'False' as const },
  ];

  test('In matches an existing condition with the expected status', () => {
    assert.equal(
      matchesPodCondition({ type: 'DisruptionTarget' }, conditions),
      true,
    );
    assert.equal(
      matchesPodCondition({ type: 'Ready', status: 'False' }, conditions),
      true,
    );
  });

  test('a missing status defaults to True', () => {
    assert.equal(
      matchesPodCondition({ type: 'Ready' }, conditions),
      false,
    );
  });

  test('NotIn matches when the condition is absent or has another status', () => {
    assert.equal(
      matchesPodCondition(
        { type: 'Initialized', operator: 'NotIn' },
        conditions,
      ),
      true,
    );
    assert.equal(
      matchesPodCondition(
        { type: 'Ready', status: 'True', operator: 'NotIn' },
        conditions,
      ),
      true,
    );
  });
});

describe('ruleMatches', () => {
  test('all present requirements must match (AND semantics)', () => {
    const rule: PodFailurePolicyRule = {
      action: 'Ignore',
      onExitCodes: { containerName: 'app', operator: 'In', values: [42] },
      onPodConditions: [{ type: 'DisruptionTarget' }],
    };
    assert.equal(
      ruleMatches(rule, failure({ conditions: [{ type: 'DisruptionTarget', status: 'True' }] })),
      true,
    );
    assert.equal(ruleMatches(rule, failure({ conditions: [] })), false);
  });

  test('a rule without requirements is a catch-all', () => {
    const rule: PodFailurePolicyRule = { action: 'FailJob' };
    assert.equal(ruleMatches(rule, failure()), true);
  });
});

describe('evaluateFailurePolicy', () => {
  const policy: PodFailurePolicy = {
    rules: [
      {
        action: 'Ignore',
        onExitCodes: { containerName: 'app', operator: 'In', values: [99] },
      },
      {
        action: 'FailIndex',
        onPodConditions: [{ type: 'DisruptionTarget' }],
      },
      { action: 'FailJob' },
    ],
  };

  test('returns the first matching rule even when later rules also match', () => {
    const matched = evaluateFailurePolicy(
      policy,
      failure({
        containers: [container('app', 99)],
        conditions: [{ type: 'DisruptionTarget', status: 'True' }],
      }),
    );
    assert.ok(matched);
    assert.equal(matched?.ruleIndex, 0);
    assert.equal(matched?.rule.action, 'Ignore');
  });

  test('skips non-matching rules and selects a later matching one', () => {
    const matched = evaluateFailurePolicy(
      policy,
      failure({
        containers: [container('app', 42)],
        conditions: [{ type: 'DisruptionTarget', status: 'True' }],
      }),
    );
    assert.equal(matched?.ruleIndex, 1);
    assert.equal(matched?.rule.action, 'FailIndex');
  });

  test('falls through to the catch-all rule', () => {
    const matched = evaluateFailurePolicy(
      policy,
      failure({ containers: [container('app', 42)], conditions: [] }),
    );
    assert.equal(matched?.ruleIndex, 2);
    assert.equal(matched?.rule.action, 'FailJob');
  });

  test('returns undefined without a policy or without a match', () => {
    assert.equal(evaluateFailurePolicy(undefined, failure()), undefined);
    const noMatch: PodFailurePolicy = {
      rules: [
        {
          action: 'Ignore',
          onExitCodes: { operator: 'In', values: [123] },
        },
      ],
    };
    assert.equal(evaluateFailurePolicy(noMatch, failure()), undefined);
  });
});
