import type { AttributeValue } from './condition.ts';

/** Legacy envelope: a flat, unscoped attribute bag. */
export interface AttributeEnvelopeV1 {
  readonly version: 1;
  readonly attributes: Readonly<Record<string, unknown>>;
}

/** Current envelope: attributes grouped into named scopes. */
export interface AttributeEnvelopeV2 {
  readonly version: 2;
  readonly scopes: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
}

export type AttributeEnvelope = AttributeEnvelopeV1 | AttributeEnvelopeV2;

export class EnvelopeError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'EnvelopeError';
  }
}

function assertValidValue(name: string, value: unknown): asserts value is AttributeValue {
  if (typeof value === 'boolean') return;
  if (typeof value === 'number' && Number.isInteger(value)) return;
  if (Array.isArray(value) && value.every((item) => typeof item === 'string')) return;
  throw new EnvelopeError(
    `attribute "${name}" must be an integer, a boolean, or a finite string set`,
  );
}

function deepFreeze<T>(value: T): T {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
  }
  return value;
}

/**
 * Normalizes any supported envelope version into a canonical, deeply frozen
 * (read-only) flat view. v1 envelopes keep their unscoped names; v2 scopes
 * are flattened to `scope.name`. The input envelope is never mutated.
 */
export function normalizeEnvelope(
  envelope: AttributeEnvelope,
): Readonly<Record<string, AttributeValue>> {
  const normalized: Record<string, AttributeValue> = {};
  if (envelope.version === 1) {
    for (const [name, value] of Object.entries(envelope.attributes)) {
      assertValidValue(name, value);
      normalized[name] = value;
    }
  } else if (envelope.version === 2) {
    for (const [scope, attributes] of Object.entries(envelope.scopes)) {
      for (const [name, value] of Object.entries(attributes)) {
        const qualified = `${scope}.${name}`;
        assertValidValue(qualified, value);
        normalized[qualified] = value;
      }
    }
  } else {
    throw new EnvelopeError(
      `unsupported envelope version: ${String((envelope as { version: unknown }).version)}`,
    );
  }
  return deepFreeze(normalized);
}
