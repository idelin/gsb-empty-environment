/**
 * 事件模型：所有状态变化都通过事件驱动，便于核对每一次输入、切换、确认与撤销。
 */
export type ControlledEvent<T> =
  /** 真实用户输入：唯一会推进 inputVersion 并记录历史的事件 */
  | { readonly type: 'input'; readonly value: T }
  /** 外部受控值同步（prop 下发）：只更新展示来源，绝不触碰内部草稿与版本 */
  | { readonly type: 'external'; readonly value: T }
  /** 切换受控 / 非受控模式 */
  | { readonly type: 'set-controlled'; readonly controlled: boolean }
  /** 外部确认（如服务端回执）：携带其所确认的输入版本号 */
  | { readonly type: 'confirm'; readonly value: T; readonly version: number }
  /** 撤销最近一次真实输入 */
  | { readonly type: 'undo' };

/** 事件被拒绝的原因 */
export type RejectReason =
  /** 确认携带的版本落后于当前输入版本，属于过期确认 */
  | 'stale-confirmation'
  /** 没有可撤销的历史 */
  | 'empty-history'
  /** 事件未引起任何状态变化（如重复切换相同模式） */
  | 'no-op';

/** 事件日志条目：记录每个事件的处置结果，供测试与调试核对 */
export interface LogEntry<T> {
  readonly seq: number;
  readonly event: ControlledEvent<T>;
  readonly applied: boolean;
  readonly reason?: RejectReason;
}
