/**
 * 持久化响应式存储：
 *  - 仅带 @persist 注解的 Signal 会被序列化进 JSON 检查点；
 *  - hydrate() 在进程重启后加载快照，恢复持久化状态、
 *    安全重置临时状态，并触发所有派生 Effect 重新对齐。
 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import { Signal, Computed, EffectHandle } from './reactive.ts';

export const CHECKPOINT_VERSION = 1;

/** @persist 注解：作为标记传入 store.signal() 声明该 Signal 需要持久化。 */
export interface PersistAnnotation {
  readonly persist: true;
}
export const persist: PersistAnnotation = Object.freeze({ persist: true });

export interface CheckpointFile {
  version: number;
  createdAt: string;
  state: Record<string, unknown>;
}

export interface HydrationReport {
  /** 从快照恢复的持久化 Signal 名称。 */
  restored: string[];
  /** 被安全重置为初始值的临时 Signal 名称。 */
  reset: string[];
  /** 快照中存在但当前进程未注册、被忽略的键。 */
  skipped: string[];
}

interface SignalEntry {
  signal: Signal<unknown>;
  persisted: boolean;
  initial: unknown;
}

function assertJsonSerializable(name: string, value: unknown): void {
  try {
    JSON.stringify(value);
  } catch (cause) {
    throw new TypeError(
      `Signal "${name}" 的值无法序列化为 JSON，无法写入检查点。`,
      { cause },
    );
  }
}

export class ReactiveStore {
  readonly #signals = new Map<string, SignalEntry>();
  readonly #effects = new Set<EffectHandle>();

  /**
   * 注册一个命名 Signal。传入 `persist` 注解（即 @persist）后，
   * 该 Signal 会被纳入检查点序列化；否则视为临时状态。
   */
  signal<T>(name: string, initial: T, annotation?: PersistAnnotation): Signal<T> {
    if (this.#signals.has(name)) {
      throw new Error(`Signal "${name}" 已注册，名称必须唯一。`);
    }
    const entry: SignalEntry = {
      signal: new Signal<unknown>(initial),
      persisted: annotation?.persist === true,
      initial,
    };
    this.#signals.set(name, entry);
    return entry.signal as Signal<T>;
  }

  /** 派生计算：惰性求值，依赖变化时自动失效并沿依赖链传播。 */
  computed<T>(derive: () => T): Computed<T> {
    return new Computed(derive);
  }

  /** 注册 Effect，立即执行一次并跟踪依赖；由 store 统一管理以便整体重对齐。 */
  effect(fn: () => void): EffectHandle {
    const handle = new EffectHandle(fn);
    this.#effects.add(handle);
    return handle;
  }

  /** 生成 JSON 检查点：仅序列化带 @persist 注解的 Signal。 */
  checkpoint(filePath: string): CheckpointFile {
    const state: Record<string, unknown> = {};
    for (const [name, entry] of this.#signals) {
      if (!entry.persisted) continue;
      const value = entry.signal.get();
      assertJsonSerializable(name, value);
      state[name] = value;
    }
    const snapshot: CheckpointFile = {
      version: CHECKPOINT_VERSION,
      createdAt: new Date().toISOString(),
      state,
    };
    mkdirSync(dirname(filePath), { recursive: true });
    writeFileSync(filePath, JSON.stringify(snapshot, null, 2), 'utf8');
    return snapshot;
  }

  /**
   * 增量 Hydration：加载检查点恢复持久化状态。
   * 1. 快照中的持久化键通过 signal.set() 写回，沿依赖链触发派生求值；
   * 2. 未持久化的临时 Signal 一律重置为初始值；
   * 3. 最后强制重对齐所有 Effect，保证重启后依赖链完整重新求值。
   */
  hydrate(filePath: string): HydrationReport {
    const raw = readFileSync(filePath, 'utf8');
    const snapshot = JSON.parse(raw) as CheckpointFile;
    if (snapshot.version !== CHECKPOINT_VERSION) {
      throw new Error(
        `不兼容的检查点版本：期望 ${CHECKPOINT_VERSION}，实际 ${snapshot.version}。`,
      );
    }

    const restored: string[] = [];
    const reset: string[] = [];
    const skipped: string[] = [];

    for (const [key, value] of Object.entries(snapshot.state)) {
      const entry = this.#signals.get(key);
      if (!entry || !entry.persisted) {
        skipped.push(key);
        continue;
      }
      entry.signal.set(value);
      restored.push(key);
    }

    for (const [name, entry] of this.#signals) {
      if (!entry.persisted) {
        entry.signal.set(entry.initial);
        reset.push(name);
      }
    }

    this.realign();

    return { restored, reset, skipped };
  }

  /** 强制重新执行所有 Effect，重建依赖图并完成派生链求值。 */
  realign(): void {
    for (const handle of this.#effects) handle.realign();
  }
}
