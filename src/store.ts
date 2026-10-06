import type { ControlledEvent, LogEntry } from './events.ts';
import type { ControlledOptions, ControlledState, ReduceResult } from './reducer.ts';
import { initialState, reduce } from './reducer.ts';

export type Listener<T> = (value: T, state: ControlledState<T>) => void;

/**
 * 受控值状态容器。
 *
 * 防反馈循环的两道闸门：
 * 1. reducer 层面：external 事件永不推进 inputVersion，自身输入的回显不会产生新版本；
 * 2. store 层面：仅当派生展示值真正变化时才通知订阅者，受控下的输入、
 *    等值回显、被拒绝的事件都不会触发通知，订阅者可以安全地回写外部值。
 */
export class ControlledValueStore<T> {
  private state: ControlledState<T>;
  private readonly log: LogEntry<T>[] = [];
  private readonly listeners = new Set<Listener<T>>();
  private readonly equals: (a: T, b: T) => boolean;

  constructor(options: ControlledOptions<T> & { equals?: (a: T, b: T) => boolean }) {
    this.state = initialState(options);
    this.equals = options.equals ?? Object.is;
  }

  getState(): ControlledState<T> {
    return this.state;
  }

  /** 当前展示值：受控时为外部值，非受控时为内部草稿 */
  getValue(): T {
    return this.state.value;
  }

  /** 完整事件日志（含被拒绝的事件及原因），用于核对行为 */
  getLog(): readonly LogEntry<T>[] {
    return this.log;
  }

  dispatch(event: ControlledEvent<T>): ReduceResult<T> {
    const previous = this.state;
    const result = reduce(previous, event);
    this.log.push({ seq: this.log.length, event, applied: result.applied, reason: result.reason });
    this.state = result.state;
    if (result.applied && !this.equals(previous.value, result.state.value)) {
      for (const listener of [...this.listeners]) {
        listener(result.state.value, result.state);
      }
    }
    return result;
  }

  subscribe(listener: Listener<T>): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** 记录一次真实用户输入 */
  input(value: T): ReduceResult<T> {
    return this.dispatch({ type: 'input', value });
  }

  /** 同步外部受控值（prop 下发） */
  syncExternal(value: T): ReduceResult<T> {
    return this.dispatch({ type: 'external', value });
  }

  /** 切换受控 / 非受控模式 */
  setControlled(controlled: boolean): ReduceResult<T> {
    return this.dispatch({ type: 'set-controlled', controlled });
  }

  /** 外部确认某个版本的输入；版本过期将被拒绝 */
  confirm(value: T, version: number): ReduceResult<T> {
    return this.dispatch({ type: 'confirm', value, version });
  }

  /** 撤销最近一次真实输入 */
  undo(): ReduceResult<T> {
    return this.dispatch({ type: 'undo' });
  }
}
