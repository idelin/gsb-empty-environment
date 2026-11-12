/**
 * 错误码：每个错误都对应一条恢复安全不变量被违反的情形。
 */
export type RestoreErrorCode =
  | 'UNKNOWN_OPERATION'
  | 'OPERATION_CANCELLED'
  | 'OPERATION_CONSUMED'
  | 'OPERATION_BLOCKED'
  | 'CONTROLLER_FROZEN'
  | 'SNAPSHOT_STALE'
  | 'RESTORE_IN_PROGRESS';

export class DocumentRestoreError extends Error {
  readonly code: RestoreErrorCode;

  constructor(code: RestoreErrorCode, message: string) {
    super(message);
    this.name = 'DocumentRestoreError';
    this.code = code;
  }
}
