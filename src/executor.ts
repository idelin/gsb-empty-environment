import { AbortError, ReceiptLostError, VersionConflictError } from './errors.ts';
import type { VersionStore } from './store.ts';
import type { PublishReceipt } from './types.ts';

export interface PublishCommand {
  readonly operationId: string;
  readonly path: string;
  readonly expectedVersion: number | null;
  readonly digest: string;
}

/**
 * 执行器：把一条发布指令投递到存储侧并取回确认回执。
 * 确认点 = compareAndSwap 成功；此后取消不再生效。
 */
export interface PublishExecutor {
  deliver(command: PublishCommand, signal: AbortSignal): Promise<PublishReceipt>;
}

function delay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new AbortError());
      return;
    }
    const onAbort = (): void => {
      clearTimeout(timer);
      reject(new AbortError());
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, ms);
    signal.addEventListener('abort', onAbort, { once: true });
  });
}

/** 直接对 VersionStore 做 CAS 提交的执行器 */
export class StoreBackedExecutor implements PublishExecutor {
  private readonly store: VersionStore;

  constructor(store: VersionStore) {
    this.store = store;
  }

  deliver(command: PublishCommand, signal: AbortSignal): Promise<PublishReceipt> {
    if (signal.aborted) {
      return Promise.reject(new AbortError());
    }
    const committed = this.store.compareAndSwap(
      command.path,
      command.expectedVersion,
      command.digest,
      command.operationId,
    );
    if (!committed) {
      const current = this.store.get(command.path);
      return Promise.reject(new VersionConflictError(command.path, current?.version ?? null));
    }
    return Promise.resolve({
      operationId: command.operationId,
      path: committed.path,
      version: committed.version,
      digest: committed.digest,
      committedAt: committed.committedAt,
    });
  }
}

export interface DeliveryScript {
  /** 投递前注入的延迟（毫秒），可被取消信号中断 */
  readonly delayMs?: number;
  /**
   * 注入失败：
   * - 'before-commit'：确认点前失败，存储无副作用；
   * - 'receipt-loss'：提交生效但确认回执丢失。
   */
  readonly failure?: 'before-commit' | 'receipt-loss';
}

/**
 * 测试用执行器：包装真实执行器，按 operationId / path 注入延迟与失败。
 */
export class ScriptedExecutor implements PublishExecutor {
  private readonly inner: PublishExecutor;
  private readonly byOperation = new Map<string, DeliveryScript>();
  private readonly byPath = new Map<string, DeliveryScript>();
  private readonly fallback: DeliveryScript;
  /** 已尝试投递的 operationId，便于断言执行次数 */
  readonly attempts: string[] = [];

  constructor(inner: PublishExecutor, fallback: DeliveryScript = {}) {
    this.inner = inner;
    this.fallback = fallback;
  }

  setScriptForOperation(operationId: string, script: DeliveryScript): void {
    this.byOperation.set(operationId, script);
  }

  setScriptForPath(path: string, script: DeliveryScript): void {
    this.byPath.set(path, script);
  }

  async deliver(command: PublishCommand, signal: AbortSignal): Promise<PublishReceipt> {
    this.attempts.push(command.operationId);
    const script =
      this.byOperation.get(command.operationId) ??
      this.byPath.get(command.path) ??
      this.fallback;
    if (script.delayMs !== undefined && script.delayMs > 0) {
      await delay(script.delayMs, signal);
    }
    if (script.failure === 'before-commit') {
      throw new Error(`injected failure before commit: ${command.operationId}`);
    }
    if (script.failure === 'receipt-loss') {
      // 用独立信号提交，确保提交本身生效；随后丢失回执。
      await this.inner.deliver(command, new AbortController().signal);
      throw new ReceiptLostError(command.operationId);
    }
    return this.inner.deliver(command, signal);
  }
}
