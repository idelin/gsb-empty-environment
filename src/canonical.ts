import { Rational } from './rational.ts';
import type { SignedWorkModule } from './types.ts';

function stableValue(value: unknown): unknown {
  if (value instanceof Rational) {
    return { __rational__: [value.n.toString(), value.d.toString()] };
  }
  if (typeof value === 'bigint') {
    return { __bigint__: value.toString() };
  }
  if (Array.isArray(value)) return value.map(stableValue);
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return Object.keys(record)
      .sort()
      .reduce<Record<string, unknown>>((acc, key) => {
        const item = record[key];
        if (item !== undefined && typeof item !== 'function') acc[key] = stableValue(item);
        return acc;
      }, {});
  }
  return value;
}

export function stableStringify(value: unknown): string {
  return JSON.stringify(stableValue(value));
}

export function moduleSigningPayload(module: SignedWorkModule): string {
  const { signature: _signature, ...payload } = module;
  return stableStringify(payload);
}
