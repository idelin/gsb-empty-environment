export type ClipboardMode = 'read' | 'write';

/**
 * 发起许可请求那一刻的环境快照。
 * 文档代随页面导航递增，焦点代随标签页焦点切换全局递增。
 */
export interface EnvironmentSnapshot {
  readonly tabId: string;
  readonly documentGeneration: number;
  readonly focusGeneration: number;
}

/** 用户手势产生的瞬态激活，带独立过期时间。 */
export interface ActivationToken {
  readonly tokenId: string;
  readonly gestureId: string;
  readonly tabId: string;
  readonly issuedAt: number;
  readonly expiresAt: number;
}

/** 发给许可器的请求：与文档代、焦点代快照绑定。 */
export interface ClipboardPermissionRequest {
  readonly requestId: string;
  readonly mode: ClipboardMode;
  readonly snapshot: EnvironmentSnapshot;
  readonly activationTokenId: string;
}

/** 许可器回调返回的授权凭据，按请求一一发放、单次消费。 */
export interface PermissionGrant {
  readonly grantId: string;
  readonly requestId: string;
  readonly mode: ClipboardMode;
  readonly snapshot: EnvironmentSnapshot;
  readonly issuedAt: number;
}

export interface ClipboardAccessContext {
  readonly grantId: string;
  readonly requestId: string;
  readonly mode: ClipboardMode;
  readonly tabId: string;
}

/** 系统剪贴板抽象：只有守卫在全部复核通过后才允许调用。 */
export interface SystemClipboard {
  read(context: ClipboardAccessContext): string;
  write(text: string, context: ClipboardAccessContext): void;
}

export interface Permissioner {
  requestPermission(
    request: ClipboardPermissionRequest,
  ): Promise<PermissionGrant>;
  /** 成功完成一次剪贴板访问后登记消费。 */
  markConsumed(grant: PermissionGrant): void;
  /** 复核失败或动作取消时作废授权，作废后同样不可再使用。 */
  invalidateGrant(grant: PermissionGrant, reason: string): void;
  /** 授权是否已不可再用（已消费或已作废）。 */
  isConsumed(grantId: string): boolean;
}

export type AuditReason =
  | 'request-created'
  | 'no-activation'
  | 'tab-missing'
  | 'permission-denied'
  | 'aborted-while-waiting'
  | 'grant-abandoned-after-abort'
  | 'recheck-failed'
  | 'grant-consumed'
  | 'clipboard-operation';

export interface AuditEntry {
  readonly at: number;
  readonly requestId: string;
  readonly reason: AuditReason;
  readonly mode: ClipboardMode;
  readonly tabId: string;
  readonly detail: Readonly<Record<string, string | number | boolean>>;
}
