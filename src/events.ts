/**
 * 事件模型：外部只能派发命令事件（StoreEvent），
 * 每次 dispatch 恰好产生一条日志事实（JournalRecord），
 * 测试与调用方通过日志核对每一次状态迁移。
 */

/** 命令事件：描述“希望发生什么” */
export type StoreEvent<T> =
  /** 用户真实输入 */
  | { readonly type: 'input'; readonly value: T }
  /** 父级下发受控值（仅受控模式下生效） */
  | { readonly type: 'external'; readonly value: T }
  /** 切换受控/非受控模式 */
  | { readonly type: 'setControlled'; readonly controlled: boolean }
  /** 外部确认：已持久化到 version 为止的输入（带版本，旧确认会被拒绝） */
  | { readonly type: 'confirm'; readonly version: number }
  /** 撤销最近一次真实输入 */
  | { readonly type: 'undo' };

/** 日志事实：描述“实际发生了什么” */
export type JournalRecord<T> =
  /** 输入被接受，版本递增 */
  | { readonly kind: 'input-accepted'; readonly value: T; readonly version: number }
  /** 外部值被应用 */
  | { readonly kind: 'external-applied'; readonly value: T }
  /** 外部值与当前外部值相同（回显/重复下发），被抑制以避免反馈循环 */
  | { readonly kind: 'external-echo-suppressed'; readonly value: T }
  /** 非受控模式下收到外部值，忽略 */
  | { readonly kind: 'external-ignored-uncontrolled'; readonly value: T }
  /** 模式切换；value 为切换后的展示值 */
  | { readonly kind: 'mode-changed'; readonly controlled: boolean; readonly value: T }
  /** 确认被接受 */
  | { readonly kind: 'confirmed'; readonly version: number }
  /** 确认被拒绝：确认版本落后于当前输入版本，旧确认不能覆盖新输入 */
  | { readonly kind: 'confirm-rejected'; readonly version: number; readonly currentVersion: number }
  /** 撤销生效，回退到上一份草稿 */
  | { readonly kind: 'undo-applied'; readonly value: T; readonly version: number }
  /** 无可撤销历史 */
  | { readonly kind: 'undo-noop' };

export type StoreEventType = StoreEvent<unknown>['type'];
export type JournalKind = JournalRecord<unknown>['kind'];

/**
 * 重入结果：在 onChange 回调中同步派发的事件不会立即执行，
 * 而是排队到当前迁移完成后按序应用（真实记录仍会写入日志）。
 */
export interface QueuedRecord {
  readonly kind: 'event-queued';
}

export type DispatchResult<T> = JournalRecord<T> | QueuedRecord;
