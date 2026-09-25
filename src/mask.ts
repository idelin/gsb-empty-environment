export const MASKED = Object.freeze({ kind: "masked" as const });

export type MaskedMarker = typeof MASKED;

export function isMasked(value: unknown): value is MaskedMarker {
  return value === MASKED;
}
