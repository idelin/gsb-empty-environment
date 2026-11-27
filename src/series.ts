import type { Attributes } from './types.js';

/**
 * Build a deterministic series key from the metric name and the point
 * attributes. Attribute keys are sorted so insertion order cannot split one
 * logical series into two cache entries.
 */
export function seriesKey(metricName: string, attributes?: Attributes): string {
  if (!attributes) {
    return `${metricName}|`;
  }
  const keys = Object.keys(attributes).sort();
  const parts = keys.map((key) => {
    const value = attributes[key];
    if (value === undefined) {
      return `${escapeKey(key)}=`;
    }
    return `${escapeKey(key)}=${encodeValue(value)}`;
  });
  return `${metricName}|${parts.join(',')}`;
}

function escapeKey(key: string): string {
  return key.replace(/\\/g, '\\\\').replace(/=/g, '\\=').replace(/,/g, '\\,');
}

function encodeValue(value: Attributes[string]): string {
  if (value === undefined) {
    return '';
  }
  if (Array.isArray(value)) {
    const scalars = value.filter(
      (item): item is string | number | boolean => item !== undefined,
    );
    return `[${scalars.map((item) => encodeScalar(item)).join(';')}]`;
  }
  return encodeScalar(value as string | number | boolean);
}

function encodeScalar(value: string | number | boolean): string {
  return typeof value === 'string' ? `"${value.replace(/"/g, '\\"')}"` : String(value);
}
