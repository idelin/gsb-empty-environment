import { Interval } from './interval';
import { Rational } from './rational';

/**
 * 提供者每次 refine 返回三选一：
 * - interval: 收紧后的新区间（必须 ⊆ 上一区间）
 * - exact:    精确证书——真实值就是该有理数（此后不再 refine）
 * - exhausted: 提供者无法再给出更多信息
 */
export type Refinement =
  | { readonly kind: 'interval'; readonly interval: Interval }
  | { readonly kind: 'exact'; readonly value: Rational }
  | { readonly kind: 'exhausted' };

export interface IntervalProvider {
  readonly id: string;
  refine(): Refinement;
  cancel(): void;
}

export type ContractViolation =
  | 'invalid-interval'
  | 'not-tightening'
  | 'exact-outside-interval'
  | 'refine-after-exact'
  | 'refine-after-exhausted';

/** 提供者违反契约（区间不合法、未单调收紧、证书越界、终止后仍 refine）时抛出。 */
export class ProviderContractError extends Error {
  readonly providerId: string;
  readonly violation: ContractViolation;

  constructor(providerId: string, violation: ContractViolation, detail: string) {
    super(`provider "${providerId}" violated contract (${violation}): ${detail}`);
    this.name = 'ProviderContractError';
    this.providerId = providerId;
    this.violation = violation;
  }
}
