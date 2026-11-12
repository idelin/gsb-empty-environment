import { DocumentRestoreError } from './errors.ts';

/** 许可状态。授予与否完全由宿主决定，组件自身从不授予任何能力。 */
export type PermissionState = 'granted' | 'denied' | 'prompt';

/** 待处理操作在其生命周期内的状态。 */
export type OperationStatus =
  | 'pending'
  | 'cancelled'
  | 'consumed'
  | 'blocked';

/**
 * TypeScript 宿主必须实现的接口：
 * - queryPermission 在恢复时重新查询当前许可（不触发新的授权提示）；
 * - runProtectedTask 在确认许可仍有效后执行受保护任务。
 */
export interface RestoreHost {
  queryPermission(capability: string): Promise<PermissionState>;
  runProtectedTask(operationId: string): Promise<void>;
}

/** 页面冻结时仍在等待执行的受保护操作。 */
export interface PendingOperation {
  id: string;
  /** 该操作需要的能力名称，例如 'clipboard-write'。 */
  capability: string;
  /** 冻结时刻宿主看到的权限代次，用于检测冻结期间的代次变化。 */
  generation: number;
  status: OperationStatus;
}

/** 普通显示状态，不含任何能力，可安全恢复。 */
export type DisplayState = Record<string, unknown>;

/** 冻结快照：记录权限代次与待处理操作，本身不携带任何可执行能力。 */
export interface FrozenSnapshot {
  readonly snapshotId: string;
  readonly frozenAt: number;
  /** capability -> 冻结时刻的权限代次。 */
  readonly permissionGenerations: Readonly<Record<string, number>>;
  readonly pending: ReadonlyArray<PendingOperation>;
  readonly displayState: DisplayState;
}

export type BlockReason =
  | 'permission-revoked'
  | 'generation-changed'
  | 'operation-cancelled'
  | 'operation-consumed'
  | 'operation-unknown';

export interface BlockedOperation {
  operationId: string;
  capability: string;
  reason: BlockReason;
}

export interface RestoreReport {
  snapshotId: string;
  /** 重复恢复事件：旧快照未被重新解冻。 */
  duplicate: boolean;
  /** 普通显示状态始终可恢复。 */
  displayState: DisplayState;
  resumed: string[];
  blocked: BlockedOperation[];
  skippedConsumed: string[];
  skippedCancelled: string[];
}

export interface RestoreEventMap {
  restored: RestoreReport;
  blocked: BlockedOperation;
}

interface RegistryEntry {
  status: OperationStatus;
  capability: string;
}

type EventHandler<K extends keyof RestoreEventMap> = (
  event: RestoreEventMap[K],
) => void;

function cloneDisplayState(state: DisplayState): DisplayState {
  if (typeof structuredClone === 'function') {
    return structuredClone(state);
  }
  return JSON.parse(JSON.stringify(state)) as DisplayState;
}

function randomId(prefix: string): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${prefix}-${crypto.randomUUID()}`;
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * 文档恢复组件。
 *
 * 安全模型：
 * 1. 冻结只做记录，不改变任何权限；
 * 2. 恢复时重新向宿主复核当前许可与代次，绝不凭快照授予能力；
 * 3. 已消费的操作不会被重发；
 * 4. 同一快照的重复恢复事件被识别并忽略；
 * 5. 冻结期间的修改入口全部关闭，防止篡改待处理操作。
 */
export class DocumentRestoreController {
  private readonly host: RestoreHost;
  private readonly registry = new Map<string, RegistryEntry>();
  private readonly generations = new Map<string, number>();
  private readonly restoredSnapshots = new Set<string>();
  private frozen = false;
  private restoreInProgress = false;
  private readonly handlers: {
    [K in keyof RestoreEventMap]?: Set<EventHandler<K>>;
  } = {};

  constructor(host: RestoreHost) {
    this.host = host;
  }

  on<K extends keyof RestoreEventMap>(
    event: K,
    handler: EventHandler<K>,
  ): () => void {
    const set = (this.handlers[event] ??= new Set()) as Set<EventHandler<K>>;
    set.add(handler);
    return () => set.delete(handler);
  }

  private emit<K extends keyof RestoreEventMap>(
    event: K,
    payload: RestoreEventMap[K],
  ): void {
    const set = this.handlers[event];
    if (set) {
      for (const handler of [...set]) {
        handler(payload);
      }
    }
  }

  isFrozen(): boolean {
    return this.frozen;
  }

  /** 注册一个待处理的受保护操作，必须在页面冻结前调用。 */
  registerPending(
    operationId: string,
    capability: string,
    generation: number,
  ): void {
    this.assertNotFrozen();
    if (!this.generations.has(capability)) {
      this.generations.set(capability, generation);
    }
    this.registry.set(operationId, { status: 'pending', capability });
  }

  /** 用户取消待处理操作；取消在冻结期排队，恢复时不会重新调度。 */
  cancel(operationId: string): void {
    this.assertNotFrozen();
    const entry = this.registry.get(operationId);
    if (!entry) {
      throw new DocumentRestoreError(
        'UNKNOWN_OPERATION',
        `未知操作，无法取消: ${operationId}`,
      );
    }
    if (entry.status === 'consumed') {
      throw new DocumentRestoreError(
        'OPERATION_CONSUMED',
        `操作已消费，无法取消: ${operationId}`,
      );
    }
    entry.status = 'cancelled';
  }

  /** 标记操作已消费（例如请求已经实际发出）。消费记录在恢复后仍然保留。 */
  markConsumed(operationId: string): void {
    this.assertNotFrozen();
    const entry = this.registry.get(operationId);
    if (!entry) {
      throw new DocumentRestoreError(
        'UNKNOWN_OPERATION',
        `未知操作，无法标记消费: ${operationId}`,
      );
    }
    entry.status = 'consumed';
  }

  /**
   * 页面进入 bfcache（pagefreeze）：记录权限代次与待处理操作。
   * 快照是不可变的纯数据，不包含任何可执行能力。
   */
  freeze(displayState: DisplayState = {}): FrozenSnapshot {
    this.assertNotFrozen();
    const permissionGenerations: Record<string, number> = {};
    const pending: PendingOperation[] = [];

    for (const [id, entry] of this.registry) {
      const generation = this.generations.get(entry.capability);
      if (generation === undefined) {
        throw new DocumentRestoreError(
          'UNKNOWN_OPERATION',
          `能力缺少代次记录，无法冻结: ${entry.capability}`,
        );
      }
      permissionGenerations[entry.capability] = generation;
      pending.push({
        id,
        capability: entry.capability,
        generation,
        status: entry.status,
      });
    }

    const snapshot: FrozenSnapshot = Object.freeze({
      snapshotId: randomId('snapshot'),
      frozenAt: Date.now(),
      permissionGenerations: Object.freeze(permissionGenerations),
      pending: Object.freeze(pending.map((item) => Object.freeze({ ...item }))),
      displayState: Object.freeze(cloneDisplayState(displayState)),
    });

    this.frozen = true;
    return snapshot;
  }

  /**
   * 页面从 bfcache 恢复（pageshow.persisted）。
   *
   * 先复核宿主当前许可与代次，再决定是否解冻受保护任务；
   * 普通显示状态无条件恢复；已消费操作绝不重发；重复事件不重复解冻。
   */
  async restore(snapshot: FrozenSnapshot): Promise<RestoreReport> {
    if (this.restoreInProgress) {
      throw new DocumentRestoreError(
        'RESTORE_IN_PROGRESS',
        '恢复流程尚未结束，拒绝并发恢复',
      );
    }

    const baseReport: RestoreReport = {
      snapshotId: snapshot.snapshotId,
      duplicate: false,
      displayState: cloneDisplayState(snapshot.displayState),
      resumed: [],
      blocked: [],
      skippedConsumed: [],
      skippedCancelled: [],
    };

    // 重复恢复事件：同一份旧快照只能解冻一次。
    if (this.restoredSnapshots.has(snapshot.snapshotId)) {
      const duplicateReport = { ...baseReport, duplicate: true };
      this.emit('restored', duplicateReport);
      return duplicateReport;
    }

    this.restoreInProgress = true;
    try {
      const capabilities = [
        ...new Set(snapshot.pending.map((item) => item.capability)),
      ];
      const currentPermissions = new Map<string, PermissionState>();
      const currentGenerations = new Map<string, number>();
      for (const capability of capabilities) {
        currentPermissions.set(
          capability,
          await this.host.queryPermission(capability),
        );
        currentGenerations.set(
          capability,
          this.generations.get(capability) ?? NaN,
        );
      }

      for (const operation of snapshot.pending) {
        const entry = this.registry.get(operation.id);

        if (!entry) {
          this.block(baseReport, operation, 'operation-unknown');
          continue;
        }

        // 复核期间状态可能再次变化，以注册表当前状态为准。
        if (entry.status === 'consumed') {
          baseReport.skippedConsumed.push(operation.id);
          continue;
        }
        if (entry.status === 'cancelled') {
          baseReport.skippedCancelled.push(operation.id);
          continue;
        }

        const currentState = currentPermissions.get(operation.capability);
        const currentGeneration = currentGenerations.get(
          operation.capability,
        );

        if (
          currentGeneration === undefined ||
          Number.isNaN(currentGeneration) ||
          currentGeneration !== operation.generation
        ) {
          entry.status = 'blocked';
          this.block(baseReport, operation, 'generation-changed');
          continue;
        }
        if (currentState !== 'granted') {
          entry.status = 'blocked';
          this.block(baseReport, operation, 'permission-revoked');
          continue;
        }

        // 复核通过：此时才允许执行受保护任务。
        await this.host.runProtectedTask(operation.id);
        entry.status = 'consumed';
        baseReport.resumed.push(operation.id);
      }

      this.restoredSnapshots.add(snapshot.snapshotId);
      this.frozen = false;
      this.emit('restored', baseReport);
      return baseReport;
    } finally {
      this.restoreInProgress = false;
    }
  }

  /** 宿主在后台撤销许可时调用，推进权限代次；冻结期同样可调用。 */
  revokeCapability(capability: string): void {
    const current = this.generations.get(capability) ?? 0;
    this.generations.set(capability, current + 1);
    for (const [id, entry] of this.registry) {
      if (entry.capability === capability && entry.status === 'pending') {
        entry.status = 'blocked';
      }
    }
  }

  /** 宿主在授权状态变化时调用，登记当前权限代次。 */
  recordGeneration(capability: string, generation: number): void {
    this.generations.set(capability, generation);
  }

  getGeneration(capability: string): number | undefined {
    return this.generations.get(capability);
  }

  private block(
    report: RestoreReport,
    operation: PendingOperation,
    reason: BlockReason,
  ): void {
    const blocked: BlockedOperation = {
      operationId: operation.id,
      capability: operation.capability,
      reason,
    };
    report.blocked.push(blocked);
    this.emit('blocked', blocked);
  }

  private assertNotFrozen(): void {
    if (this.frozen) {
      throw new DocumentRestoreError(
        'CONTROLLER_FROZEN',
        '文档处于冻结状态，拒绝变更待处理操作',
      );
    }
  }
}
