import type { ControlledEvent, RejectReason } from './events.ts';

/** 历史快照：撤销时恢复的内部草稿与其版本 */
interface Snapshot<T> {
  readonly draft: T;
  readonly inputVersion: number;
}

/**
 * 受控值状态。
 *
 * 不变量：
 * - `draft` 永远是最近一次真实用户输入（或初始默认值），外部同步与确认拒绝都不会覆盖它；
 * - `inputVersion` 只随真实输入（及撤销）变化，外部值回显不会推进版本；
 * - `value` 为派生展示值：受控时以外部值为准，非受控时展示内部草稿。
 */
export interface ControlledState<T> {
  readonly controlled: boolean;
  /** 外部下发的受控值 */
  readonly externalValue: T;
  /** 最近一次真实输入（内部默认值演进而来的草稿） */
  readonly draft: T;
  /** 真实输入的版本号，每次输入 +1 */
  readonly inputVersion: number;
  /** 已被外部确认的版本号，单调不减 */
  readonly confirmedVersion: number;
  /** 派生展示值：controlled ? externalValue : draft */
  readonly value: T;
  /** 撤销历史（栈顶为最近一次输入前的状态） */
  readonly history: readonly Snapshot<T>[];
}

export interface ControlledOptions<T> {
  /** 内部默认值（非受控模式的初始值） */
  readonly defaultValue: T;
  /** 受控模式下的外部值 */
  readonly value?: T;
  /** 初始是否受控；缺省时传入了 value 即视为受控 */
  readonly controlled?: boolean;
}

export function initialState<T>(options: ControlledOptions<T>): ControlledState<T> {
  const controlled = options.controlled ?? options.value !== undefined;
  const externalValue = options.value !== undefined ? options.value : options.defaultValue;
  return {
    controlled,
    externalValue,
    draft: options.defaultValue,
    inputVersion: 0,
    confirmedVersion: 0,
    value: controlled ? externalValue : options.defaultValue,
    history: [],
  };
}

export interface ReduceResult<T> {
  readonly state: ControlledState<T>;
  readonly applied: boolean;
  readonly reason?: RejectReason;
}

function derive<T>(state: Omit<ControlledState<T>, 'value'>): ControlledState<T> {
  return { ...state, value: state.controlled ? state.externalValue : state.draft };
}

function rejected<T>(state: ControlledState<T>, reason: RejectReason): ReduceResult<T> {
  return { state, applied: false, reason };
}

/** 纯函数归约器：给定状态与事件，返回新状态及处置结果 */
export function reduce<T>(state: ControlledState<T>, event: ControlledEvent<T>): ReduceResult<T> {
  switch (event.type) {
    case 'input': {
      // 真实输入：记录历史、更新草稿、推进版本。受控时展示值不变（外部值优先）。
      const history = [...state.history, { draft: state.draft, inputVersion: state.inputVersion }];
      return {
        applied: true,
        state: derive({ ...state, draft: event.value, inputVersion: state.inputVersion + 1, history }),
      };
    }
    case 'external': {
      // 外部同步：只更新外部值。不触碰 draft / inputVersion / history，
      // 因此自身输入的回显（echo）不会被误认为新输入，从源头避免反馈循环。
      if (Object.is(state.externalValue, event.value)) {
        return rejected(state, 'no-op');
      }
      return { applied: true, state: derive({ ...state, externalValue: event.value }) };
    }
    case 'set-controlled': {
      if (state.controlled === event.controlled) {
        return rejected(state, 'no-op');
      }
      // 切入受控：展示切换为外部值，草稿保留；切出受控：展示恢复为最近真实输入。
      return { applied: true, state: derive({ ...state, controlled: event.controlled }) };
    }
    case 'confirm': {
      // 过期确认：版本落后于当前输入版本，拒绝，不得覆盖更新的输入。
      if (event.version < state.inputVersion) {
        return rejected(state, 'stale-confirmation');
      }
      const confirmedVersion = Math.max(state.confirmedVersion, event.version);
      return { applied: true, state: derive({ ...state, draft: event.value, confirmedVersion }) };
    }
    case 'undo': {
      const snapshot = state.history[state.history.length - 1];
      if (snapshot === undefined) {
        return rejected(state, 'empty-history');
      }
      const history = state.history.slice(0, -1);
      return {
        applied: true,
        state: derive({ ...state, draft: snapshot.draft, inputVersion: snapshot.inputVersion, history }),
      };
    }
  }
}
