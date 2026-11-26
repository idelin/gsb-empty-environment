import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  AdmissionFailureError,
  OscillationConflictError,
  runAdmissionPipeline,
} from '../src/index.js';
import {
  addAt,
  annotationsOf,
  baseObject,
  labelsOf,
  removeAt,
  replicasOf,
  setAt,
  simpleWebhook,
  traceSignature,
} from './helpers.js';
import type { JsonObject, JsonPatchOperation, TraceEntry } from '../src/types.js';

test('later mutation triggers earlier IfNeeded reinvoke, while Never is not reinvoked', () => {
  const calls: string[] = [];
  const webhooks = [
    simpleWebhook({
      name: 'defaults.ifneeded',
      reinvocationPolicy: 'IfNeeded',
      handler: ({ object }) => {
        calls.push('defaults.ifneeded');
        const annotation = annotationsOf(object);
        const patch: JsonPatchOperation[] = [];
        if (annotation['injected-by'] === undefined) {
          patch.push(addAt('/metadata/annotations/injected-by', 'defaults.ifneeded'));
        }
        const expectedSynced = (annotation['last-writer'] as string | undefined) ?? 'none';
        if (annotation.synced !== expectedSynced) {
          if (annotation.synced === undefined) {
            patch.push(addAt('/metadata/annotations/synced', expectedSynced));
          } else {
            patch.push(setAt('/metadata/annotations/synced', expectedSynced));
          }
        }
        return {
          allowed: true,
          patch,
        };
      },
    }),
    simpleWebhook({
      name: 'sidecar.never',
      reinvocationPolicy: 'Never',
      handler: ({ object }) => {
        calls.push('sidecar.never');
        const template = (object.spec as JsonObject).template as JsonObject;
        const podSpec = template.spec as JsonObject;
        const containers = podSpec.containers as JsonObject[];
        if (containers.some((container) => container.name === 'sidecar')) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/spec/template/spec/containers/-', { name: 'sidecar' })] };
      },
    }),
    simpleWebhook({
      name: 'writer.ifneeded',
      reinvocationPolicy: 'IfNeeded',
      handler: () => {
        calls.push('writer.ifneeded');
        return {
          allowed: true,
          patch: [setAt('/metadata/annotations/last-writer', 'writer.ifneeded')],
        };
      },
    }),
  ];

  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks,
    maxReinvocationRounds: 3,
  });

  assert.deepEqual(calls, [
    'defaults.ifneeded',
    'sidecar.never',
    'writer.ifneeded',
    'defaults.ifneeded',
    'writer.ifneeded',
    'defaults.ifneeded',
  ]);
  assert.equal(result.roundsExecuted, 3);
  assert.deepEqual(result.invocations, {
    'defaults.ifneeded': 3,
    'sidecar.never': 1,
    'writer.ifneeded': 2,
  });

  const annotations = annotationsOf(result.object);
  assert.equal(annotations['injected-by'], 'defaults.ifneeded');
  assert.equal(annotations['last-writer'], 'writer.ifneeded');
  assert.equal(annotations.synced, 'writer.ifneeded');

  const expected: Array<[number, string, string]> = [
    [0, 'defaults.ifneeded', 'allowed'],
    [0, 'sidecar.never', 'allowed'],
    [0, 'writer.ifneeded', 'allowed'],
    [1, 'defaults.ifneeded', 'allowed'],
    [1, 'sidecar.never', 'skipped'],
    [1, 'writer.ifneeded', 'no-change'],
    [2, 'defaults.ifneeded', 'no-change'],
    [2, 'sidecar.never', 'skipped'],
    [2, 'writer.ifneeded', 'skipped'],
  ];
  assert.deepEqual(traceSignature(result.trace), expected);
  const trace = result.trace as TraceEntry[];
  assert.equal(trace[4]?.action, 'skip-never-reinvoke');
  assert.equal(trace[5]?.outcome, 'no-change');
  assert.equal(trace[7]?.action, 'skip-never-reinvoke');
  assert.equal(trace[8]?.action, 'skip-no-trigger');
});

test('a middle webhook removing the matching label causes later webhooks to be skipped', () => {
  const calls: string[] = [];
  const tierSelector = { matchLabels: { tier: 'web' } };
  const webhooks = [
    simpleWebhook({
      name: 'label-remover',
      objectSelector: tierSelector,
      handler: () => {
        calls.push('label-remover');
        return { allowed: true, patch: [removeAt('/metadata/labels/tier')] };
      },
    }),
    simpleWebhook({
      name: 'tier-only-a',
      objectSelector: tierSelector,
      handler: () => {
        calls.push('tier-only-a');
        return { allowed: true, patch: [setAt('/spec/replicas', 2)] };
      },
    }),
    simpleWebhook({
      name: 'tier-only-b',
      objectSelector: tierSelector,
      handler: () => {
        calls.push('tier-only-b');
        return { allowed: true, patch: [setAt('/spec/replicas', 3)] };
      },
    }),
  ];

  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks,
    maxReinvocationRounds: 2,
  });

  assert.deepEqual(calls, ['label-remover']);
  assert.equal(result.roundsExecuted, 1);
  assert.equal(replicasOf(result.object), 1);
  assert.deepEqual(
    traceSignature(result.trace),
    [
      [0, 'label-remover', 'allowed'],
      [0, 'tier-only-a', 'skipped'],
      [0, 'tier-only-b', 'skipped'],
    ],
  );
  assert.equal((result.trace as TraceEntry[])[1]?.action, 'skip-selector-miss');
  assert.equal((result.trace as TraceEntry[])[2]?.action, 'skip-selector-miss');
});

test('a webhook whose selector newly matches on a later pass runs once and the chain converges', () => {
  const calls: string[] = [];
  const webhooks = [
    simpleWebhook({
      name: 'bootstrap.ifneeded',
      reinvocationPolicy: 'IfNeeded',
      handler: ({ object }) => {
        calls.push('bootstrap.ifneeded');
        if (annotationsOf(object).bootstrapped !== undefined) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/metadata/annotations/bootstrapped', 'true')] };
      },
    }),
    simpleWebhook({
      name: 'delayed-label-enabler',
      reinvocationPolicy: 'IfNeeded',
      handler: ({ object }) => {
        calls.push('delayed-label-enabler');
        const annotations = annotationsOf(object);
        if (labelsOf(object).feature !== undefined) {
          return { allowed: true };
        }
        if (annotations.bootstrapped === undefined) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/metadata/labels/feature', 'on')] };
      },
    }),
    simpleWebhook({
      name: 'feature-gated',
      objectSelector: { matchLabels: { feature: 'on' } },
      reinvocationPolicy: 'IfNeeded',
      handler: ({ object }) => {
        calls.push('feature-gated');
        const spec = object.spec as JsonObject;
        if (spec.featureActive !== undefined) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/spec/featureActive', true)] };
      },
    }),
  ];

  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks,
    maxReinvocationRounds: 3,
  });

  assert.deepEqual(calls, [
    'bootstrap.ifneeded',
    'delayed-label-enabler',
    'feature-gated',
    'bootstrap.ifneeded',
  ]);
  assert.equal(result.roundsExecuted, 2);
  assert.equal(labelsOf(result.object).feature, 'on');
  assert.equal((result.object.spec as JsonObject).featureActive, true);
  assert.deepEqual(traceSignature(result.trace), [
    [0, 'bootstrap.ifneeded', 'allowed'],
    [0, 'delayed-label-enabler', 'allowed'],
    [0, 'feature-gated', 'allowed'],
    [1, 'bootstrap.ifneeded', 'no-change'],
    [1, 'delayed-label-enabler', 'skipped'],
    [1, 'feature-gated', 'skipped'],
  ]);
  const trace = result.trace as TraceEntry[];
  assert.equal(trace[4]?.action, 'skip-no-trigger');
  assert.equal(trace[5]?.action, 'skip-no-trigger');
});

test('a selector that misses in pass 0 can match in a later pass and runs exactly once', () => {
  const calls: string[] = [];
  const gatedName = 'second-pass-gated';
  const webhooks = [
    simpleWebhook({
      // Registered first so on the re-invocation pass it observes the pending
      // mutation before any inert webhook can clear the flag. Inert on pass 0,
      // adds the gating label on its first re-invocation.
      name: 'second-pass-labeler',
      reinvocationPolicy: 'IfNeeded',
      handler: ({ object }, context) => {
        calls.push('second-pass-labeler');
        if (context.invocationCount === 0 || labelsOf(object).ready !== undefined) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/metadata/labels/ready', 'true')] };
      },
    }),
    simpleWebhook({
      // Mutates on pass 0 to admit a re-invocation pass; inert afterwards.
      name: 'first-pass-mutator',
      reinvocationPolicy: 'IfNeeded',
      handler: (_, context) => {
        calls.push('first-pass-mutator');
        if (context.invocationCount > 0) {
          return { allowed: true };
        }
        return { allowed: true, patch: [addAt('/metadata/annotations/pass-zero', 'done')] };
      },
    }),
    simpleWebhook({
      name: gatedName,
      objectSelector: { matchLabels: { ready: 'true' } },
      reinvocationPolicy: 'IfNeeded',
      handler: () => {
        calls.push(gatedName);
        return { allowed: true, patch: [addAt('/spec/featureActive', true)] };
      },
    }),
  ];

  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks,
    maxReinvocationRounds: 3,
  });

  assert.deepEqual(calls, [
    'second-pass-labeler',
    'first-pass-mutator',
    'second-pass-labeler',
    'first-pass-mutator',
    gatedName,
    'second-pass-labeler',
  ]);
  assert.equal(result.roundsExecuted, 3);
  assert.equal(labelsOf(result.object).ready, 'true');
  assert.equal((result.object.spec as JsonObject).featureActive, true);
  assert.deepEqual(traceSignature(result.trace), [
    [0, 'second-pass-labeler', 'no-change'],
    [0, 'first-pass-mutator', 'allowed'],
    [0, gatedName, 'skipped'],
    [1, 'second-pass-labeler', 'allowed'],
    [1, 'first-pass-mutator', 'no-change'],
    [1, gatedName, 'allowed'],
    [2, 'second-pass-labeler', 'no-change'],
    [2, 'first-pass-mutator', 'skipped'],
    [2, gatedName, 'skipped'],
  ]);
  const trace = result.trace as TraceEntry[];
  assert.equal(trace[2]?.action, 'skip-selector-miss');
  assert.equal(trace[7]?.action, 'skip-no-trigger');
  assert.equal(trace[8]?.action, 'skip-no-trigger');
});

test('a failing second patch operation rolls back the whole batch and Ignore continues', () => {
  const calls: string[] = [];
  const webhooks = [
    simpleWebhook({
      name: 'broken-batch',
      failurePolicy: 'Ignore',
      handler: () => {
        calls.push('broken-batch');
        return {
          allowed: true,
          patch: [
            setAt('/spec/replicas', 9),
            addAt('/spec/template/spec/missing/child', 'boom'),
          ],
        };
      },
    }),
    simpleWebhook({
      name: 'healthy',
      handler: () => {
        calls.push('healthy');
        return { allowed: true, patch: [setAt('/spec/replicas', 4)] };
      },
    }),
  ];

  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks,
    maxReinvocationRounds: 1,
  });

  assert.deepEqual(calls, ['broken-batch', 'healthy']);
  assert.equal(replicasOf(result.object), 4);
  assert.deepEqual(traceSignature(result.trace), [
    [0, 'broken-batch', 'patch-failed-ignored'],
    [0, 'healthy', 'allowed'],
  ]);
  const failed = (result.trace as TraceEntry[])[0];
  assert.ok(failed?.detail?.includes('patch operation 1 failed'));
  assert.deepEqual(failed?.touchedPaths, []);
});

test('the same rollback under Fail aborts the pipeline with an AdmissionFailureError', () => {
  const webhooks = [
    simpleWebhook({
      name: 'broken-batch-fail',
      failurePolicy: 'Fail',
      handler: () => ({
        allowed: true,
        patch: [
          setAt('/spec/replicas', 9),
          addAt('/spec/template/spec/missing/child', 'boom'),
        ],
      }),
    }),
    simpleWebhook({
      name: 'must-not-run',
      handler: () => ({ allowed: true, patch: [setAt('/spec/replicas', 4)] }),
    }),
  ];

  assert.throws(
    () => runAdmissionPipeline({ object: baseObject(), webhooks, maxReinvocationRounds: 1 }),
    (error: unknown) => {
      assert.ok(error instanceof AdmissionFailureError);
      assert.equal(error.kind, 'patch-failed');
      assert.equal(error.webhookName, 'broken-batch-fail');
      assert.equal(replicasOf(error.object), 1);
      const names = error.trace.map((entry) => entry.webhookName);
      assert.deepEqual(names, ['broken-batch-fail']);
      return true;
    },
  );
});

test('denied Ignore webhook is recorded and the chain continues', () => {
  const result = runAdmissionPipeline({
    object: baseObject(),
    webhooks: [
      simpleWebhook({
        name: 'denier-ignore',
        failurePolicy: 'Ignore',
        handler: () => ({ allowed: false, statusMessage: 'not today' }),
      }),
      simpleWebhook({
        name: 'after-denier',
        handler: () => ({ allowed: true, patch: [setAt('/spec/replicas', 7)] }),
      }),
    ],
    maxReinvocationRounds: 1,
  });

  assert.equal(replicasOf(result.object), 7);
  assert.deepEqual(traceSignature(result.trace), [
    [0, 'denier-ignore', 'denied-ignored'],
    [0, 'after-denier', 'allowed'],
  ]);
});

test('a zero reinvocation budget reports a round-limit conflict after the initial pass', () => {
  assert.throws(
    () =>
      runAdmissionPipeline({
        object: baseObject(),
        maxReinvocationRounds: 0,
        webhooks: [
          simpleWebhook({
            name: 'mutator-a',
            reinvocationPolicy: 'IfNeeded',
            handler: () => ({ allowed: true, patch: [setAt('/spec/replicas', 2)] }),
          }),
          simpleWebhook({
            name: 'mutator-b',
            reinvocationPolicy: 'IfNeeded',
            handler: () => ({ allowed: true, patch: [setAt('/spec/replicas', 3)] }),
          }),
        ],
      }),
    (error: unknown) => {
      assert.ok(error instanceof OscillationConflictError);
      assert.equal(error.details.reason, 'round-limit');
      assert.equal(error.details.roundsExecuted, 1);
      assert.deepEqual(error.details.participatingWebhooks, ['mutator-a', 'mutator-b']);
      assert.deepEqual(error.details.conflictPaths, ['/spec/replicas']);
      return true;
    },
  );
});

test('a thrown handler error under Ignore is recorded and the chain keeps going', () => {
  const result = runAdmissionPipeline({
    object: baseObject(),
    maxReinvocationRounds: 1,
    webhooks: [
      simpleWebhook({
        name: 'throws-ignore',
        failurePolicy: 'Ignore',
        handler: () => {
          throw new Error('kaboom');
        },
      }),
      simpleWebhook({
        name: 'after-throw',
        handler: () => ({ allowed: true, patch: [setAt('/spec/replicas', 7)] }),
      }),
    ],
  });

  assert.equal(replicasOf(result.object), 7);
  assert.deepEqual(traceSignature(result.trace), [
    [0, 'throws-ignore', 'error-ignored'],
    [0, 'after-throw', 'allowed'],
  ]);
  assert.equal((result.trace as TraceEntry[])[0]?.detail, 'kaboom');
});

test('two webhooks flipping the same key trigger an exact state-cycle conflict', () => {
  const flip = (target: string, value: 'a' | 'b'): JsonPatchOperation[] => [
    setAt('/metadata/labels/mode', value),
    setAt('/metadata/annotations/owner', target),
  ];

  const webhooks = [
    simpleWebhook({
      name: 'flapper-a',
      reinvocationPolicy: 'IfNeeded',
      handler: () => ({ allowed: true, patch: flip('flapper-a', 'a') }),
    }),
    simpleWebhook({
      name: 'flapper-b',
      reinvocationPolicy: 'IfNeeded',
      handler: () => ({ allowed: true, patch: flip('flapper-b', 'b') }),
    }),
  ];

  const initial = baseObject();
  labelsOf(initial).mode = 'init';
  annotationsOf(initial).owner = 'init';

  assert.throws(
    () => runAdmissionPipeline({ object: initial, webhooks, maxReinvocationRounds: 10 }),
    (error: unknown) => {
      assert.ok(error instanceof OscillationConflictError);
      assert.equal(error.details.reason, 'state-cycle');
      assert.deepEqual(error.details.participatingWebhooks, ['flapper-b', 'flapper-a']);
      assert.deepEqual(error.details.conflictPaths, [
        '/metadata/labels/mode',
        '/metadata/annotations/owner',
      ]);
      assert.equal(error.details.repeatedRound, 1);
      assert.deepEqual(error.details.repeatedState, {
        ...initial,
        metadata: {
          ...(initial.metadata as JsonObject),
          labels: { ...labelsOf(initial), mode: 'a' },
          annotations: { ...annotationsOf(initial), owner: 'flapper-a' },
        },
      });
      assert.equal(
        labelsOf(error.object).mode,
        'a',
        'object stays at the pre-cycle state because the repeating transition is rejected',
      );
      return true;
    },
  );
});

test('exhausting the reinvocation budget reports a round-limit conflict with actors and paths', () => {
  const counterHandler = (delta: number) => ({ object }: { object: JsonObject }) => {
    const labels = labelsOf(object);
    const counter = Number(labels.counter ?? 0);
    const operation =
      labels.counter === undefined
        ? addAt('/metadata/labels/counter', counter + delta)
        : setAt('/metadata/labels/counter', counter + delta);
    return { allowed: true, patch: [operation] };
  };
  const webhooks = [
    simpleWebhook({
      name: 'never-settles-a',
      reinvocationPolicy: 'IfNeeded',
      handler: counterHandler(1),
    }),
    simpleWebhook({
      name: 'never-settles-b',
      reinvocationPolicy: 'IfNeeded',
      handler: counterHandler(10),
    }),
  ];

  assert.throws(
    () => runAdmissionPipeline({ object: baseObject(), webhooks, maxReinvocationRounds: 1 }),
    (error: unknown) => {
      assert.ok(error instanceof OscillationConflictError);
      assert.equal(error.details.reason, 'round-limit');
      assert.equal(error.details.roundsExecuted, 2);
      assert.deepEqual(error.details.participatingWebhooks, [
        'never-settles-a',
        'never-settles-b',
      ]);
      assert.deepEqual(error.details.conflictPaths, ['/metadata/labels/counter']);
      return true;
    },
  );
});
