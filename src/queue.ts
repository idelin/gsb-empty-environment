import { randomUUID } from 'node:crypto';
import { ReceiptLostError, VersionConflictError, isAbortError } from './errors.ts';
import type { PublishCommand, PublishExecutor } from './executor.ts';
import { InMemoryJournal } from './journal.ts';
import type { Journal } from './journal.ts';
import type { FileVersion, VersionStore } from './store.ts';
import type {
  PublishHandle,
  PublishOutcome,
  PublishReceipt,
  PublishRequest,
  PublishStatus,
} from './types.ts';

export interface PublishQueueDeps {
  readonly executor: PublishExecutor;
  readonly store: VersionStore;
  readonly journal?: Journal;
}

interface OperationRecord {
  readonly operationId: string;
  readonly request: Required<PublishRequest>;
  status: PublishStatus;
  outcome?: PublishOutcome;
  readonly abort: AbortController;
  readonly resolve: (outcome: PublishOutcome) => void;
  readonly promise: Promise<PublishOutcome>;
  handle: PublishHandle;
}

function versionMatches(current: FileVersion | undefined, expected: number | null): boolean {
  return expected === null ? current === undefined : current?.version === expected;
}

function receiptFrom(file: FileVersion): PublishReceipt {
  return {
    operationId: file.operationId,
    path: file.path,
    version: file.version,
    digest: file.digest,
    committedAt: file.committedAt,
  };
}

function settledHandle(operationId: string, path: string, outcome: PublishOutcome): PublishHandle {
  return {
    operationId,
    path,
    promise: Promise.resolve(outcome),
    status: () => outcome.status,
    cancel: () => outcome.status,
  };
}

/**
 * 文件发布队列：
 * - 同路径操作串行链式执行，不同路径互不阻塞；
 * - 确认点（执行器提交成功）前可取消，确认后取消返回已提交；
 * - 终态写入日志，恢复 / 幂等重放不重新执行。
 */
export class PublishQueue {
  private readonly executor: PublishExecutor;
  private readonly store: VersionStore;
  private readonly journal: Journal;
  private readonly tails = new Map<string, Promise<void>>();
  private readonly operations = new Map<string, OperationRecord>();

  constructor(deps: PublishQueueDeps) {
    this.executor = deps.executor;
    this.store = deps.store;
    this.journal = deps.journal ?? new InMemoryJournal();
  }

  submit(request: PublishRequest): PublishHandle {
    const operationId = request.operationId ?? randomUUID();
    const existing = this.operations.get(operationId);
    if (existing) {
      return existing.handle;
    }
    const journaled = this.journal.get(operationId);
    if (journaled) {
      // 幂等重放：同一 operationId 直接返回已记录的结果
      return settledHandle(operationId, request.path, journaled);
    }

    let resolveFn: (outcome: PublishOutcome) => void = () => undefined;
    const promise = new Promise<PublishOutcome>((res) => {
      resolveFn = res;
    });
    const record: OperationRecord = {
      operationId,
      request: { ...request, operationId },
      status: 'queued',
      abort: new AbortController(),
      resolve: resolveFn,
      promise,
      handle: undefined as unknown as PublishHandle,
    };
    record.handle = {
      operationId,
      path: request.path,
      promise,
      status: () => record.status,
      cancel: () => this.cancel(record),
    };
    this.operations.set(operationId, record);

    // 同路径串行：挂到该路径的执行链尾部
    const previous = this.tails.get(request.path) ?? Promise.resolve();
    const run = previous.then(() => this.execute(record));
    this.tails.set(request.path, run);
    return record.handle;
  }

  /**
   * 恢复：优先查日志中的终态；日志缺失时按幂等键向存储对账，
   * 找回已提交但未记录的结果。均不重新执行。
   */
  recover(operationId: string): PublishOutcome | undefined {
    const live = this.operations.get(operationId);
    if (live?.outcome) {
      return live.outcome;
    }
    const journaled = this.journal.get(operationId);
    if (journaled) {
      return journaled;
    }
    const committed = this.store.findByOperationId(operationId);
    if (committed) {
      const outcome: PublishOutcome = { status: 'committed', receipt: receiptFrom(committed) };
      this.journal.record(operationId, outcome);
      return outcome;
    }
    return undefined;
  }

  private async execute(record: OperationRecord): Promise<void> {
    if (record.outcome) {
      return; // 排队期间已被取消
    }
    record.status = 'running';
    const { path, expectedVersion, digest } = record.request;

    const current = this.store.get(path);
    if (!versionMatches(current, expectedVersion)) {
      this.settle(record, { status: 'conflict', currentVersion: current?.version ?? null });
      return;
    }

    const command: PublishCommand = {
      operationId: record.operationId,
      path,
      expectedVersion,
      digest,
    };
    try {
      const receipt = await this.executor.deliver(command, record.abort.signal);
      this.settle(record, { status: 'committed', receipt });
    } catch (error) {
      if (error instanceof ReceiptLostError) {
        // 回执丢失：按幂等键对账，确认点已过则恢复已提交结果
        const committed = this.store.findByOperationId(record.operationId);
        if (committed) {
          this.settle(record, { status: 'committed', receipt: receiptFrom(committed) });
        } else {
          this.settle(record, { status: 'failed', error });
        }
      } else if (error instanceof VersionConflictError) {
        this.settle(record, { status: 'conflict', currentVersion: error.currentVersion });
      } else if (isAbortError(error)) {
        this.settle(record, { status: 'cancelled' });
      } else {
        this.settle(record, {
          status: 'failed',
          error: error instanceof Error ? error : new Error(String(error)),
        });
      }
    }
  }

  private cancel(record: OperationRecord): PublishStatus {
    if (record.outcome) {
      // 终态不可变更：已提交就返回已提交，不能宣称撤销
      return record.status;
    }
    if (record.status === 'queued') {
      this.settle(record, { status: 'cancelled' });
      return record.status;
    }
    // 执行中：发出中止信号，确认点前有效；最终状态以 promise 为准
    record.abort.abort();
    return record.status;
  }

  private settle(record: OperationRecord, outcome: PublishOutcome): void {
    if (record.outcome) {
      return;
    }
    record.outcome = outcome;
    record.status = outcome.status;
    this.journal.record(record.operationId, outcome);
    record.resolve(outcome);
  }
}
