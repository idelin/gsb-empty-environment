import {
  AdmissionFailureError,
  OscillationConflictError,
  type OscillationDetails,
} from './errors.js';
import {
  applyJsonPatch,
  canonicalJson,
  deepClone,
  JsonPatchError,
} from './json-pointer.js';
import { matchesSelector } from './selector.js';
import type {
  AdmissionResponse,
  JsonObject,
  PipelineInput,
  PipelineResult,
  TraceEntry,
  WebhookDefinition,
} from './types.js';

interface HistoryEntry {
  state: string;
  snapshot: JsonObject;
  round: number;
  index: number;
  webhookName: string;
  touchedPaths: string[];
}

const KNOWN_OPS = new Set(['add', 'remove', 'replace', 'move', 'copy', 'test']);

function validateInput(input: PipelineInput): void {
  if (input === null || typeof input !== 'object') {
    throw new TypeError('pipeline input must be an object');
  }
  if (input.object === null || typeof input.object !== 'object' || Array.isArray(input.object)) {
    throw new TypeError('pipeline input.object must be a JSON object');
  }
  if (!Number.isInteger(input.maxReinvocationRounds) || input.maxReinvocationRounds < 0) {
    throw new TypeError('maxReinvocationRounds must be a non-negative integer');
  }
  if (!Array.isArray(input.webhooks)) {
    throw new TypeError('pipeline input.webhooks must be an array');
  }
  const seen = new Set<string>();
  for (const webhook of input.webhooks) {
    validateWebhook(webhook, seen);
  }
}

function validateWebhook(webhook: WebhookDefinition, seen: Set<string>): void {
  if (webhook === null || typeof webhook !== 'object') {
    throw new TypeError('every webhook must be an object');
  }
  if (typeof webhook.name !== 'string' || webhook.name === '') {
    throw new TypeError('every webhook requires a non-empty string name');
  }
  if (seen.has(webhook.name)) {
    throw new TypeError(`duplicate webhook name: ${webhook.name}`);
  }
  seen.add(webhook.name);
  if (webhook.reinvocationPolicy !== 'Never' && webhook.reinvocationPolicy !== 'IfNeeded') {
    throw new TypeError(`webhook ${webhook.name} has invalid reinvocationPolicy`);
  }
  if (webhook.failurePolicy !== 'Fail' && webhook.failurePolicy !== 'Ignore') {
    throw new TypeError(`webhook ${webhook.name} has invalid failurePolicy`);
  }
  if (typeof webhook.handler !== 'function') {
    throw new TypeError(`webhook ${webhook.name} requires a handler function`);
  }
}

function objectLabels(object: JsonObject): Record<string, string> {
  const metadata = object.metadata;
  if (metadata !== null && typeof metadata === 'object' && !Array.isArray(metadata)) {
    const labels = (metadata as JsonObject).labels;
    if (labels !== null && typeof labels === 'object' && !Array.isArray(labels)) {
      return labels as Record<string, string>;
    }
  }
  return {};
}

function unionPaths(paths: readonly string[][]): string[] {
  const result: string[] = [];
  for (const group of paths) {
    for (const path of group) {
      if (!result.includes(path)) {
        result.push(path);
      }
    }
  }
  return result;
}

function validateResponse(response: unknown, webhookName: string): AdmissionResponse {
  if (response === null || typeof response !== 'object') {
    throw new Error(`webhook ${webhookName} returned a non-object response`);
  }
  const candidate = response as AdmissionResponse;
  if (typeof candidate.allowed !== 'boolean') {
    throw new Error(`webhook ${webhookName} response requires a boolean "allowed"`);
  }
  if (candidate.patch !== undefined) {
    if (!Array.isArray(candidate.patch)) {
      throw new Error(`webhook ${webhookName} response.patch must be an array`);
    }
    candidate.patch.forEach((operation, index) => {
      if (operation === null || typeof operation !== 'object') {
        throw new Error(`webhook ${webhookName} patch[${index}] must be an object`);
      }
      if (typeof operation.path !== 'string' || !KNOWN_OPS.has(operation.op)) {
        throw new Error(`webhook ${webhookName} patch[${index}] is malformed`);
      }
    });
  }
  return candidate;
}

/**
 * Execute the ordered mutating admission pipeline.
 *
 * Pass 0 invokes every webhook whose selectors match. A live `changed` flag
 * (the Kubernetes dispatcher model) records whether the most recently invoked
 * webhook produced a semantic mutation: it propagates across passes and within
 * a pass, so an `IfNeeded` webhook re-runs whenever some webhook earlier in
 * the current chain has mutated the object since it last ran. `Never`
 * webhooks run at most once, first-time matches may enter on a later pass when
 * a selector newly matches, and selectors are always evaluated against the
 * live object. State cycles and exhausted re-invocation budgets raise
 * {@link OscillationConflictError}.
 */
export function runAdmissionPipeline(input: PipelineInput): PipelineResult {
  validateInput(input);

  const namespaceLabels = input.namespaceLabels ?? {};
  let current: JsonObject = deepClone(input.object);
  const trace: TraceEntry[] = [];
  const invocationCounts = new Map<string, number>();
  const invoked = new Set<number>();

  const history: HistoryEntry[] = [
    {
      state: canonicalJson(current),
      snapshot: deepClone(current),
      round: -1,
      index: -1,
      webhookName: '',
      touchedPaths: [],
    },
  ];

  let round = 0;
  // Live "a mutation is pending re-invocation" flag. It starts true when a new
  // pass is admitted because the preceding pass mutated the object, is then
  // overwritten by the result of each *invoked* webhook within the pass, and
  // gates IfNeeded re-invocations (mirrors the Kubernetes dispatcher).
  let changed = false;

  const pushTrace = (entry: TraceEntry): void => {
    trace.push(entry);
  };

  const selectorsMatch = (webhook: WebhookDefinition): boolean =>
    matchesSelector(webhook.objectSelector, objectLabels(current)) &&
    matchesSelector(webhook.namespaceSelector, namespaceLabels);

  const raiseOscillation = (
    reason: OscillationDetails['reason'],
    participatingWebhooks: string[],
    conflictPaths: string[],
    extra: Partial<OscillationDetails> = {},
  ): never => {
    const details: OscillationDetails = {
      reason,
      participatingWebhooks,
      conflictPaths,
      roundsExecuted: round + 1,
      trace,
      object: current,
      ...extra,
    };
    const message =
      reason === 'state-cycle'
        ? `oscillation conflict: object returned to a previously observed canonical state via webhooks [${participatingWebhooks.join(' -> ')}]; conflict paths: [${conflictPaths.join(', ')}]`
        : `oscillation conflict: re-invocation budget of ${input.maxReinvocationRounds} exhausted while webhooks [${participatingWebhooks.join(' -> ')}] kept mutating; conflict paths: [${conflictPaths.join(', ')}]`;
    throw new OscillationConflictError(message, details);
  };

  const converge = (roundsExecuted: number): PipelineResult => ({
    object: current,
    trace,
    roundsExecuted,
    invocations: Object.fromEntries(invocationCounts.entries()),
  });

  // eslint-disable-next-line no-constant-condition
  while (true) {
    changed = round === 0 ? false : true;
    let passMutated = false;

    for (let index = 0; index < input.webhooks.length; index += 1) {
      const webhook = input.webhooks[index] as WebhookDefinition;

      if (!selectorsMatch(webhook)) {
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'skip-selector-miss',
          outcome: 'skipped',
          touchedPaths: [],
          detail: 'objectSelector or namespaceSelector did not match the live object',
        });
        continue;
      }

      if (invoked.has(index)) {
        if (webhook.reinvocationPolicy === 'Never') {
          pushTrace({
            round,
            index,
            webhookName: webhook.name,
            action: 'skip-never-reinvoke',
            outcome: 'skipped',
            touchedPaths: [],
            detail: 'reinvocationPolicy is Never',
          });
          continue;
        }
        if (!changed) {
          pushTrace({
            round,
            index,
            webhookName: webhook.name,
            action: 'skip-no-trigger',
            outcome: 'skipped',
            touchedPaths: [],
            detail: 'no semantic mutation is pending re-invocation for this webhook',
          });
          continue;
        }
      }

      const invocationCount = invocationCounts.get(webhook.name) ?? 0;
      invocationCounts.set(webhook.name, invocationCount + 1);
      invoked.add(index);

      let response: AdmissionResponse;
      try {
        response = validateResponse(
          webhook.handler(
            { object: deepClone(current), namespaceLabels },
            { webhookName: webhook.name, round, invocationCount },
          ),
          webhook.name,
        );
      } catch (cause) {
        changed = false;
        const detail = cause instanceof Error ? cause.message : String(cause);
        if (webhook.failurePolicy === 'Fail') {
          pushTrace({
            round,
            index,
            webhookName: webhook.name,
            action: 'invoke',
            outcome: 'error-fail',
            touchedPaths: [],
            detail,
          });
          throw new AdmissionFailureError(
            `webhook ${webhook.name} handler failed with failurePolicy Fail: ${detail}`,
            { webhookName: webhook.name, round, kind: 'handler-error', object: current, trace },
          );
        }
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'invoke',
          outcome: 'error-ignored',
          touchedPaths: [],
          detail,
        });
        continue;
      }

      if (!response.allowed) {
        changed = false;
        const detail = response.statusMessage ?? 'webhook denied the request';
        if (webhook.failurePolicy === 'Fail') {
          pushTrace({
            round,
            index,
            webhookName: webhook.name,
            action: 'invoke',
            outcome: 'denied-fail',
            touchedPaths: [],
            detail,
          });
          throw new AdmissionFailureError(
            `webhook ${webhook.name} denied the request with failurePolicy Fail: ${detail}`,
            { webhookName: webhook.name, round, kind: 'denied', object: current, trace },
          );
        }
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'invoke',
          outcome: 'denied-ignored',
          touchedPaths: [],
          detail,
        });
        continue;
      }

      const patch = response.patch ?? [];
      if (patch.length === 0) {
        changed = false;
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'invoke',
          outcome: 'no-change',
          touchedPaths: [],
        });
        continue;
      }

      let applied: ReturnType<typeof applyJsonPatch>;
      try {
        applied = applyJsonPatch(current, patch);
      } catch (cause) {
        changed = false;
        const detail =
          cause instanceof JsonPatchError
            ? `patch operation ${cause.operationIndex} failed: ${cause.message}`
            : cause instanceof Error
              ? cause.message
              : String(cause);
        if (webhook.failurePolicy === 'Fail') {
          pushTrace({
            round,
            index,
            webhookName: webhook.name,
            action: 'invoke',
            outcome: 'patch-failed-fail',
            touchedPaths: [],
            detail,
          });
          throw new AdmissionFailureError(
            `webhook ${webhook.name} patch rolled back with failurePolicy Fail: ${detail}`,
            { webhookName: webhook.name, round, kind: 'patch-failed', object: current, trace },
          );
        }
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'invoke',
          outcome: 'patch-failed-ignored',
          touchedPaths: [],
          detail,
        });
        continue;
      }

      if (!applied.changed) {
        changed = false;
        pushTrace({
          round,
          index,
          webhookName: webhook.name,
          action: 'invoke',
          outcome: 'no-change',
          touchedPaths: applied.touchedPaths,
          detail: 'patch applied but produced no semantic change',
        });
        continue;
      }

      current = applied.doc as JsonObject;
      changed = true;
      passMutated = true;
      const nextState = canonicalJson(current);
      const cycleAt = history.findIndex((entry) => entry.state === nextState);

      history.push({
        state: nextState,
        snapshot: deepClone(current),
        round,
        index,
        webhookName: webhook.name,
        touchedPaths: applied.touchedPaths,
      });

      if (cycleAt >= 0) {
        const cycleStart = history[cycleAt] as HistoryEntry;
        const cycleEntries = history.slice(cycleAt + 1);
        raiseOscillation(
          'state-cycle',
          cycleEntries.map((entry) => entry.webhookName),
          unionPaths(cycleEntries.map((entry) => entry.touchedPaths)),
          {
            repeatedState: cycleStart.snapshot,
            firstSeenRound: cycleStart.round,
            repeatedRound: round,
          },
        );
      }

      pushTrace({
        round,
        index,
        webhookName: webhook.name,
        action: 'invoke',
        outcome: 'allowed',
        touchedPaths: applied.touchedPaths,
      });
    }

    if (!passMutated) {
      return converge(round + 1);
    }

    // A further pass only does work if some webhook would actually run: either
    // an uninvoked first-time match, or a matching IfNeeded webhook (Never
    // webhooks never re-run). Otherwise the chain has converged.
    const someReinvocableWillTrigger = input.webhooks.some((webhook, index) => {
      if (!invoked.has(index)) {
        return selectorsMatch(webhook);
      }
      return webhook.reinvocationPolicy === 'IfNeeded' && selectorsMatch(webhook);
    });
    if (!someReinvocableWillTrigger) {
      return converge(round + 1);
    }

    if (round >= input.maxReinvocationRounds) {
      const mutatingEntries = history.filter((entry) => entry.round === round);
      raiseOscillation(
        'round-limit',
        mutatingEntries.map((entry) => entry.webhookName),
        unionPaths(mutatingEntries.map((entry) => entry.touchedPaths)),
      );
    }

    round += 1;
  }
}
