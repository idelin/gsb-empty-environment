import type { FixedRejection } from './types.ts';

export const FIXED_REJECTION: FixedRejection = Object.freeze({
  ok: false,
  allowed: false,
  decision: 'reject',
  code: 'RELEASE_REJECTED',
});

export function fixedRejection(): FixedRejection {
  return FIXED_REJECTION;
}
