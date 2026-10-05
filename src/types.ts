export interface PublishRequest {
  /** 目标路径，同路径的发布会串行执行 */
  readonly path: string;
  /** 预期旧版本；null 表示目标必须尚不存在 */
  readonly expectedVersion: number | null;
  /** 新内容摘要 */
  readonly digest: string;
  /** 幂等键；缺省时由队列生成 */
  readonly operationId?: string;
}

export interface PublishReceipt {
  readonly operationId: string;
  readonly path: string;
  /** 提交后的新版本号 */
  readonly version: number;
  readonly digest: string;
  readonly committedAt: number;
}

export type PublishOutcome =
  | { readonly status: 'committed'; readonly receipt: PublishReceipt }
  | { readonly status: 'cancelled' }
  | { readonly status: 'conflict'; readonly currentVersion: number | null }
  | { readonly status: 'failed'; readonly error: Error };

export type PublishStatus = PublishOutcome['status'] | 'queued' | 'running';

export interface PublishHandle {
  readonly operationId: string;
  readonly path: string;
  /** 终态结果（提交 / 取消 / 冲突 / 失败） */
  readonly promise: Promise<PublishOutcome>;
  status(): PublishStatus;
  /**
   * 请求取消。仅在确认点前有效：
   * 已提交时返回 'committed'，不会宣称撤销。
   */
  cancel(): PublishStatus;
}
