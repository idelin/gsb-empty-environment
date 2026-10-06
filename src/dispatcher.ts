/**
 * KeymapDispatcher — 多键序列快捷键分发器。
 *
 * 设计要点：
 * - 只接受显式时钟事件（handleKey 的时间戳与 tick），内部不使用任何真实定时器。
 * - 短序列是长序列前缀时进入等待期，期限到达（含恰好等于）执行短命令。
 * - 失配时先执行待定短命令，再把当前键从空缓冲重新匹配。
 * - 焦点变更取消待定序列（不执行任何命令）。
 * - 上下文栈：栈顶优先级最高；高层上下文中的候选会遮蔽全局（低层）候选。
 * - 等待期间上下文/配置变化会重新裁决已输入键，但重新裁决本身绝不触发命令。
 * - 配置热更新为先校验后整体替换的原子操作；非法配置抛错且保留旧配置。
 * - 暂停/恢复不改变序列状态与代际（generation）。
 */

export interface KeyBinding {
  /** 按键序列，例如 ['g', 'g']。 */
  readonly keys: readonly string[];
  /** 命中后要执行的命令标识。 */
  readonly command: string;
  /** 所属上下文；缺省表示全局。 */
  readonly context?: string;
}

export interface KeymapConfig {
  readonly bindings: readonly KeyBinding[];
  /** 前缀等待的逻辑期限（与显式时钟同一时间轴）。 */
  readonly timeoutMs: number;
}

export type CancelReason = 'focus' | 'context' | 'config' | 'timeout';

export type DispatcherEvent =
  | { readonly type: 'execute'; readonly command: string; readonly generation: number }
  | {
      readonly type: 'pending';
      readonly keys: readonly string[];
      readonly deadline: number;
      readonly generation: number;
    }
  | { readonly type: 'cancel'; readonly reason: CancelReason; readonly generation: number };

export interface DispatcherState {
  readonly typed: readonly string[];
  readonly pendingCommand: string | null;
  readonly deadline: number | null;
  readonly generation: number;
  readonly paused: boolean;
  readonly contextStack: readonly string[];
}

interface Candidate {
  readonly binding: KeyBinding;
  /** 0 表示全局；栈中位置越靠后（越接近栈顶）优先级越高。 */
  readonly priority: number;
}

export class KeymapDispatcher {
  private config: KeymapConfig;
  private contextStack: string[] = [];
  private typed: string[] = [];
  private pendingCommand: string | null = null;
  private deadline: number | null = null;
  private generation = 0;
  private paused = false;
  private lastNow: number | null = null;
  private readonly emit: (event: DispatcherEvent) => void;

  constructor(config: KeymapConfig, emit: (event: DispatcherEvent) => void) {
    validateConfig(config);
    this.config = config;
    this.emit = emit;
  }

  /** 按键事件。`at` 为显式时钟时间，必须单调不减。 */
  handleKey(key: string, at: number): void {
    if (this.paused) return;
    this.advanceClock(at);
    this.typeKey(key, at, false);
  }

  /** 显式时钟事件。到达（含恰好等于）期限时执行待定短命令。 */
  tick(at: number): void {
    if (this.paused) return;
    this.advanceClock(at);
  }

  /** 焦点变更：取消待定序列，不执行任何命令。 */
  focusChanged(): void {
    if (this.typed.length > 0) {
      this.resetSequence('focus');
    }
  }

  /** 整体替换上下文栈，并重新裁决已输入键。 */
  setContextStack(stack: readonly string[]): void {
    this.contextStack = [...stack];
    this.readjudicate('context');
  }

  /** 压入上下文（如弹层打开），并重新裁决已输入键。 */
  pushContext(context: string): void {
    this.contextStack.push(context);
    this.readjudicate('context');
  }

  /** 弹出栈顶上下文（如弹层关闭），并重新裁决已输入键。 */
  popContext(): string | undefined {
    const popped = this.contextStack.pop();
    this.readjudicate('context');
    return popped;
  }

  /** 原子热更新配置：先校验，非法则抛错并保留旧配置；合法则整体替换并重新裁决。 */
  setConfig(config: KeymapConfig): void {
    validateConfig(config);
    this.config = config;
    this.readjudicate('config');
  }

  /** 暂停：忽略按键与时钟事件，序列状态与代际保持不动。 */
  pause(): void {
    this.paused = true;
  }

  /** 恢复：继续处理事件；待定序列的代际与暂停前一致。 */
  resume(): void {
    this.paused = false;
  }

  getState(): DispatcherState {
    return {
      typed: [...this.typed],
      pendingCommand: this.pendingCommand,
      deadline: this.deadline,
      generation: this.generation,
      paused: this.paused,
      contextStack: [...this.contextStack],
    };
  }

  private typeKey(key: string, at: number, isRematch: boolean): void {
    const fresh = this.typed.length === 0;
    this.typed.push(key);
    const candidates = this.candidatesFor(this.typed);
    if (candidates.length === 0) {
      // 失配：先执行待定短命令，再把当前键从空缓冲重新匹配（仅一次）。
      const flushed = this.pendingCommand;
      this.resetSequence();
      if (flushed !== null) {
        this.execute(flushed);
      }
      if (!isRematch) {
        this.typeKey(key, at, true);
      }
      return;
    }
    if (fresh) {
      this.generation += 1;
    }
    this.applyCandidates(candidates, at);
  }

  private applyCandidates(candidates: readonly Candidate[], at: number): void {
    let exact: KeyBinding | null = null;
    let hasLonger = false;
    for (const candidate of candidates) {
      if (candidate.binding.keys.length === this.typed.length) {
        if (exact === null) exact = candidate.binding;
      } else {
        hasLonger = true;
      }
    }
    if (exact !== null && !hasLonger) {
      // 唯一精确匹配且无更长序列：立即执行。
      this.execute(exact.command);
      this.resetSequence();
      return;
    }
    this.pendingCommand = exact === null ? null : exact.command;
    this.deadline = at + this.config.timeoutMs;
    this.emit({
      type: 'pending',
      keys: [...this.typed],
      deadline: this.deadline,
      generation: this.generation,
    });
  }

  private advanceClock(at: number): void {
    if (this.lastNow !== null && at < this.lastNow) {
      throw new Error(`clock must be monotonic: got ${at} after ${this.lastNow}`);
    }
    this.lastNow = at;
    if (this.deadline !== null && at >= this.deadline) {
      const command = this.pendingCommand;
      this.resetSequence(command === null ? 'timeout' : undefined);
      if (command !== null) {
        this.execute(command);
      }
    }
  }

  /**
   * 上下文或配置变化后重新裁决已输入键。
   * 只更新待定状态，绝不执行命令（避免重复触发）；期限保持原值。
   */
  private readjudicate(reason: CancelReason): void {
    if (this.typed.length === 0) return;
    const candidates = this.candidatesFor(this.typed);
    if (candidates.length === 0) {
      this.resetSequence(reason);
      return;
    }
    let exact: KeyBinding | null = null;
    for (const candidate of candidates) {
      if (candidate.binding.keys.length === this.typed.length) {
        if (exact === null) exact = candidate.binding;
      }
    }
    this.pendingCommand = exact === null ? null : exact.command;
  }

  private candidatesFor(typed: readonly string[]): Candidate[] {
    const matched: Candidate[] = [];
    for (const binding of this.config.bindings) {
      if (binding.keys.length < typed.length) continue;
      let isPrefix = true;
      for (let i = 0; i < typed.length; i += 1) {
        if (binding.keys[i] !== typed[i]) {
          isPrefix = false;
          break;
        }
      }
      if (!isPrefix) continue;
      const priority = this.priorityOf(binding.context);
      if (priority < 0) continue;
      matched.push({ binding, priority });
    }
    // 遮蔽：只保留最高优先级层（弹层候选遮蔽全局候选）。
    let max = -1;
    for (const candidate of matched) {
      if (candidate.priority > max) max = candidate.priority;
    }
    return matched.filter((candidate) => candidate.priority === max);
  }

  private priorityOf(context: string | undefined): number {
    if (context === undefined) return 0;
    const index = this.contextStack.lastIndexOf(context);
    return index < 0 ? -1 : index + 1;
  }

  private execute(command: string): void {
    this.emit({ type: 'execute', command, generation: this.generation });
  }

  private resetSequence(reason?: CancelReason): void {
    const hadSequence = this.typed.length > 0;
    this.typed = [];
    this.pendingCommand = null;
    this.deadline = null;
    if (reason !== undefined && hadSequence) {
      this.emit({ type: 'cancel', reason, generation: this.generation });
    }
  }
}

function validateConfig(config: KeymapConfig): void {
  if (!Number.isFinite(config.timeoutMs) || config.timeoutMs <= 0) {
    throw new Error(`invalid timeoutMs: ${config.timeoutMs}`);
  }
  if (!Array.isArray(config.bindings)) {
    throw new Error('bindings must be an array');
  }
  for (const binding of config.bindings) {
    if (!Array.isArray(binding.keys) || binding.keys.length === 0) {
      throw new Error(`binding for "${binding.command}" must have a non-empty keys array`);
    }
    for (const key of binding.keys) {
      if (typeof key !== 'string' || key.length === 0) {
        throw new Error(`binding for "${binding.command}" contains an invalid key`);
      }
    }
    if (typeof binding.command !== 'string' || binding.command.length === 0) {
      throw new Error('binding command must be a non-empty string');
    }
  }
}
