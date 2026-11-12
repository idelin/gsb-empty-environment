import type { RedemptionKey } from './types.ts';

/** 兑换被拒绝的稳定错误码，便于调用方区分攻击类型。 */
export type RedemptionErrorCode =
  | 'EMPTY_BATCH'
  | 'BATCH_TOO_LARGE'
  | 'HETEROGENEOUS_ISSUER'
  | 'UNKNOWN_ISSUER'
  | 'UNKNOWN_KEY'
  | 'KEY_NOT_YET_VALID'
  | 'KEY_EXPIRED'
  | 'INVALID_SIGNATURE'
  | 'DUPLICATE_TOKEN'
  | 'TOO_MANY_ACTIVE_ISSUERS'
  | 'RECORD_NOT_FOUND'
  | 'RECORD_EXPIRED'
  | 'RECORD_ALREADY_PRESENTED';

/** 兑换流程抛出的业务错误，携带稳定错误码与上下文。 */
export class RedemptionError extends Error {
  readonly code: RedemptionErrorCode;
  readonly details?: Record<string, unknown>;

  constructor(
    code: RedemptionErrorCode,
    message: string,
    details?: Record<string, unknown>,
  ) {
    super(message);
    this.name = 'RedemptionError';
    this.code = code;
    this.details = details;
  }
}

export function duplicateTokenError(key: RedemptionKey): RedemptionError {
  return new RedemptionError(
    'DUPLICATE_TOKEN',
    `令牌已在有效期内兑换过：${key.issuerId}/${key.keyId}#${key.tokenIndex}`,
    { key },
  );
}
