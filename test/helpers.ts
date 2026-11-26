import type {
  JsonObject,
  JsonValue,
  JsonPatchOperation,
  TraceEntry,
  WebhookDefinition,
} from '../src/types.js';

export function setAt(path: string, value: unknown): JsonPatchOperation {
  return { op: 'replace', path, value: value as JsonValue };
}

export function addAt(path: string, value: unknown): JsonPatchOperation {
  return { op: 'add', path, value: value as JsonValue };
}

export function removeAt(path: string): JsonPatchOperation {
  return { op: 'remove', path };
}

export function simpleWebhook(
  partial: Partial<WebhookDefinition> & Pick<WebhookDefinition, 'name' | 'handler'>,
): WebhookDefinition {
  return {
    reinvocationPolicy: 'Never',
    failurePolicy: 'Fail',
    ...partial,
  };
}

export function traceSignature(trace: readonly TraceEntry[]): Array<[number, string, string]> {
  return trace.map((entry) => [entry.round, entry.webhookName, entry.outcome]);
}

export function baseObject(): JsonObject {
  return {
    apiVersion: 'apps/v1',
    kind: 'Deployment',
    metadata: {
      name: 'demo',
      labels: { app: 'demo', tier: 'web' },
      annotations: { 'last-writer': 'none' },
    },
    spec: {
      replicas: 1,
      template: {
        metadata: { labels: { app: 'demo' } },
        spec: { containers: [] },
      },
    },
  };
}

export function labelsOf(object: JsonObject): Record<string, string> {
  const metadata = object.metadata;
  assertObject(metadata);
  const labels = metadata.labels;
  assertObject(labels);
  return labels as Record<string, string>;
}

export function annotationsOf(object: JsonObject): Record<string, JsonValue> {
  const metadata = object.metadata;
  assertObject(metadata);
  const annotations = metadata.annotations;
  assertObject(annotations);
  return annotations;
}

export function replicasOf(object: JsonObject): number {
  const spec = object.spec;
  assertObject(spec);
  return spec.replicas as number;
}

function assertObject(value: JsonValue | undefined): asserts value is JsonObject {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('expected a JSON object');
  }
}
