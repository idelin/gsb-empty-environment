export const ERR = {
  NotFound: 'NOT_FOUND',
  AlreadyExists: 'ALREADY_EXISTS',
  InvalidPath: 'INVALID_PATH',
  WouldCycle: 'WOULD_CYCLE',
  AliasConflict: 'ALIAS_CONFLICT',
  AliasCycle: 'ALIAS_CYCLE',
  AliasTargetMissing: 'ALIAS_TARGET_MISSING',
  AliasResolutionLimit: 'ALIAS_RESOLUTION_LIMIT',
  MountConflict: 'MOUNT_CONFLICT',
  MountCycle: 'MOUNT_CYCLE',
  RootEscape: 'ROOT_ESCAPE',
  ReadOnlyMount: 'READ_ONLY_MOUNT',
  ConfigChanged: 'CONFIG_CHANGED',
  TransactionClosed: 'TRANSACTION_CLOSED',
} as const;

export type ErrCode = (typeof ERR)[keyof typeof ERR];

export class VfsError extends Error {
  readonly code: ErrCode;

  constructor(code: ErrCode, message: string) {
    super(message);
    this.name = 'VfsError';
    this.code = code;
  }
}
