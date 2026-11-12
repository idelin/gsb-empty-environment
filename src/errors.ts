import type { EnvironmentSnapshot, ClipboardMode } from './types.ts';

export class ClipboardGuardError extends Error {
  override readonly name = 'ClipboardGuardError';
}

/** 调用动作没有绑定任何仍在有效期内的用户激活。 */
export class NoTransientActivationError extends ClipboardGuardError {
  override readonly name = 'NoTransientActivationError';
  readonly mode: ClipboardMode;
  readonly tabId: string;

  constructor(mode: ClipboardMode, tabId: string) {
    super(`no live transient activation for clipboard ${mode} on tab ${tabId}`);
    this.mode = mode;
    this.tabId = tabId;
  }
}

export class TabNotFoundError extends ClipboardGuardError {
  override readonly name = 'TabNotFoundError';
  readonly tabId: string;
  readonly snapshot: EnvironmentSnapshot;

  constructor(tabId: string, snapshot: EnvironmentSnapshot) {
    super(`tab ${tabId} no longer exists when clipboard grant arrived`);
    this.tabId = tabId;
    this.snapshot = snapshot;
  }
}

/** 许可回调到达时，标签页已经发生过导航，文档代不一致。 */
export class StaleDocumentGenerationError extends ClipboardGuardError {
  override readonly name = 'StaleDocumentGenerationError';
  readonly expected: number;
  readonly actual: number;
  readonly tabId: string;

  constructor(
    expected: number,
    actual: number,
    tabId: string,
  ) {
    super(
      `document generation mismatch on tab ${tabId}: expected ${expected}, got ${actual}`,
    );
    this.expected = expected;
    this.actual = actual;
    this.tabId = tabId;
  }
}

/** 许可回调到达时，焦点已经离开原标签页或发生过焦点切换。 */
export class StaleFocusGenerationError extends ClipboardGuardError {
  override readonly name = 'StaleFocusGenerationError';
  readonly expected: number;
  readonly actual: number;
  readonly tabId: string;

  constructor(
    expected: number,
    actual: number,
    tabId: string,
  ) {
    super(
      `focus generation mismatch on tab ${tabId}: expected ${expected}, got ${actual}`,
    );
    this.expected = expected;
    this.actual = actual;
    this.tabId = tabId;
  }
}

/** 回调到达时，发起手势的瞬态激活已经过期。 */
export class ExpiredActivationError extends ClipboardGuardError {
  override readonly name = 'ExpiredActivationError';
  readonly activationTokenId: string;
  readonly expiredAt: number;
  readonly now: number;

  constructor(
    activationTokenId: string,
    expiredAt: number,
    now: number,
  ) {
    super(
      `transient activation ${activationTokenId} expired at ${expiredAt}, now is ${now}`,
    );
    this.activationTokenId = activationTokenId;
    this.expiredAt = expiredAt;
    this.now = now;
  }
}

export class PermissionDeniedError extends ClipboardGuardError {
  override readonly name = 'PermissionDeniedError';
  readonly mode: ClipboardMode;
  readonly requestId: string;

  constructor(
    mode: ClipboardMode,
    requestId: string,
    causeMessage: string,
  ) {
    super(`clipboard ${mode} permission denied for request ${requestId}: ${causeMessage}`);
    this.mode = mode;
    this.requestId = requestId;
  }
}

export class ClipboardAbortError extends ClipboardGuardError {
  override readonly name = 'ClipboardAbortError';
  readonly requestId: string;

  constructor(requestId: string) {
    super(`clipboard request ${requestId} was aborted before completion`);
    this.requestId = requestId;
  }
}
