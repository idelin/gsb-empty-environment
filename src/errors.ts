export type HrefValidationReason =
  | 'empty'
  | 'too-long'
  | 'not-absolute'
  | 'invalid-uri'
  | 'query-or-fragment'
  | 'foreign-authority'
  | 'bad-percent-encoding'
  | 'empty-segment'
  | 'dot-segment'
  | 'encoded-separator'
  | 'control-character'
  | 'not-direct-member'
  | 'not-a-member';

export class HrefValidationError extends Error {
  reason: HrefValidationReason;

  constructor(reason: HrefValidationReason, message = reason) {
    super(message);
    this.name = 'HrefValidationError';
    this.reason = reason;
  }
}

export type InvalidSyncTokenReason =
  | 'malformed'
  | 'bad-signature'
  | 'collection-mismatch'
  | 'principal-mismatch'
  | 'version-out-of-range'
  | 'history-expired';

export class InvalidSyncTokenError extends Error {
  reason: InvalidSyncTokenReason;
  readonly status = 403;
  readonly precondition = 'valid-sync-token';

  constructor(reason: InvalidSyncTokenReason, message = reason) {
    super(message);
    this.name = 'InvalidSyncTokenError';
    this.reason = reason;
  }
}

export class SyncHistoryExpiredError extends Error {
  constructor(message = 'sync history is no longer retained') {
    super(message);
    this.name = 'SyncHistoryExpiredError';
  }
}
