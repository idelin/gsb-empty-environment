import type { DispatchResult, JournalRecord, StoreEvent } from './events';

export interface Snapshot<T> {
  /** 当前是否受控 */
  readonly controlled: boolean;
  /** 展示值：受控时以外部值为准，非受控时为内部草稿 */
  readonly value: T;
  /** 内部草稿：最近真实输入，切换受控状态时始终保留 */
  readonly draft: T;
  /** 父级下发的外部值 */
  readonly externalValue: T;
  /** 输入版本：每次真实输入（含撤销）单调递增 */
  readonly version: number;
  /** 已被外部确认的版本 */
  readonly confirmedVersion: number;
  /** 是否存在未被确认的输入 */
  readonly dirty: boolean;
  readonly canUndo: boolean;
}

export interface ControllableStoreOptions<T> {
  /** 受控值；传入（非 undefined）即初始为受控模式 */
  readonly value?: T;
  /** 内部默认值（非受控初始值） */
  readonly defaultValue: T;
  /** 仅由用户来源事件（input / undo）触发；外部同步事件绝不触发，避免反馈循环 */
  readonly onChange?: (value: T, version: number) => void;
}

export type Listener<T> = (value: T, snapshot: Snapshot<T>) => void;

export interface ControllableStore<T> {
  dispatch(event: StoreEvent<T>): DispatchResult<T>;
  getSnapshot(): Snapshot<T>;
  getJournal(): readonly JournalRecord<T>[];
  subscribe(listener: Listener<T>): () => void;
  input(value: T): DispatchResult<T>;
  syncExternal(value: T): DispatchResult<T>;
  setControlled(controlled: boolean): DispatchResult<T>;
  confirm(version: number): DispatchResult<T>;
  undo(): DispatchResult<T>;
}

class Store<T> implements ControllableStore<T> {
  private controlled: boolean;
  private draft: T;
  private externalValue: T;
  private version = 0;
  private confirmedVersion = 0;
  private readonly past: T[] = [];
  private readonly journal: JournalRecord<T>[] = [];
  private readonly listeners = new Set<Listener<T>>();
  private readonly onChange?: (value: T, version: number) => void;
  /** 重入保护：onChange 回调中同步派发的事件进入队列，当前迁移完成后依次处理 */
  private emitting = false;
  private readonly pending: StoreEvent<T>[] = [];

  constructor(options: ControllableStoreOptions<T>) {
    this.controlled = options.value !== undefined;
    this.draft = options.defaultValue;
    this.externalValue = options.value !== undefined ? options.value : options.defaultValue;
    if (options.onChange !== undefined) {
      this.onChange = options.onChange;
    }
  }

  getSnapshot(): Snapshot<T> {
    return {
      controlled: this.controlled,
      value: this.controlled ? this.externalValue : this.draft,
      draft: this.draft,
      externalValue: this.externalValue,
      version: this.version,
      confirmedVersion: this.confirmedVersion,
      dirty: this.version > this.confirmedVersion,
      canUndo: this.past.length > 0,
    };
  }

  getJournal(): readonly JournalRecord<T>[] {
    return this.journal;
  }

  subscribe(listener: Listener<T>): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  input(value: T): DispatchResult<T> {
    return this.dispatch({ type: 'input', value });
  }

  syncExternal(value: T): DispatchResult<T> {
    return this.dispatch({ type: 'external', value });
  }

  setControlled(controlled: boolean): DispatchResult<T> {
    return this.dispatch({ type: 'setControlled', controlled });
  }

  confirm(version: number): DispatchResult<T> {
    return this.dispatch({ type: 'confirm', version });
  }

  undo(): DispatchResult<T> {
    return this.dispatch({ type: 'undo' });
  }

  dispatch(event: StoreEvent<T>): DispatchResult<T> {
    if (this.emitting) {
      // 回调期间的重入事件排队，保证日志顺序可核对
      this.pending.push(event);
      return { kind: 'event-queued' };
    }
    const record = this.apply(event);
    while (this.pending.length > 0) {
      const queued = this.pending.shift();
      if (queued !== undefined) {
        this.apply(queued);
      }
    }
    return record;
  }

  private apply(event: StoreEvent<T>): JournalRecord<T> {
    const before = this.controlled ? this.externalValue : this.draft;
    const record = this.reduce(event);
    this.journal.push(record);
    const after = this.controlled ? this.externalValue : this.draft;
    if (!Object.is(before, after)) {
      this.notify();
    }
    return record;
  }

  private reduce(event: StoreEvent<T>): JournalRecord<T> {
    switch (event.type) {
      case 'input': {
        this.past.push(this.draft);
        this.draft = event.value;
        this.version += 1;
        this.emitChange(event.value, this.version);
        return { kind: 'input-accepted', value: event.value, version: this.version };
      }
      case 'external': {
        if (!this.controlled) {
          return { kind: 'external-ignored-uncontrolled', value: event.value };
        }
        if (Object.is(this.externalValue, event.value)) {
          // 回显抑制：父级把同样的值又传回来，不再扩散
          return { kind: 'external-echo-suppressed', value: event.value };
        }
        this.externalValue = event.value;
        return { kind: 'external-applied', value: event.value };
      }
      case 'setControlled': {
        this.controlled = event.controlled;
        // 草稿始终保留；展示值切换为当前模式对应的来源
        return {
          kind: 'mode-changed',
          controlled: this.controlled,
          value: this.controlled ? this.externalValue : this.draft,
        };
      }
      case 'confirm': {
        if (event.version < this.version) {
          // 旧确认不能覆盖新输入
          return { kind: 'confirm-rejected', version: event.version, currentVersion: this.version };
        }
        this.confirmedVersion = Math.max(this.confirmedVersion, event.version);
        return { kind: 'confirmed', version: event.version };
      }
      case 'undo': {
        const previous = this.past.pop();
        if (previous === undefined) {
          return { kind: 'undo-noop' };
        }
        this.draft = previous;
        this.version += 1;
        this.emitChange(previous, this.version);
        return { kind: 'undo-applied', value: previous, version: this.version };
      }
    }
  }

  /** 仅用户来源事件会触发 onChange */
  private emitChange(value: T, version: number): void {
    if (this.onChange === undefined) {
      return;
    }
    this.emitting = true;
    try {
      this.onChange(value, version);
    } finally {
      this.emitting = false;
    }
  }

  private notify(): void {
    const snapshot = this.getSnapshot();
    for (const listener of this.listeners) {
      listener(snapshot.value, snapshot);
    }
  }
}

export function createControllableStore<T>(options: ControllableStoreOptions<T>): ControllableStore<T> {
  return new Store(options);
}
