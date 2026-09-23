import { RestoreError } from './errors.ts';
import type { ComputationDef, Registry } from './registry.ts';
import {
  checkEvaluationCycles,
  checkMissingDeps,
  checkMonotonicCycles,
  markDirtyDownstream,
  recomputeAll,
  type NodeRecord,
  type RecordMap,
} from './graph.ts';
import { stageRestore, type SnapshotFile } from './serialize.ts';

export type OpBatch = readonly Op[];

export type Op =
  | { readonly op: 'defineState'; readonly id: string; readonly value: unknown }
  | { readonly op: 'set'; readonly id: string; readonly value: unknown }
  | {
      readonly op: 'defineComputed';
      readonly id: string;
      readonly defId: string;
      readonly defVersion: number;
      readonly deps: readonly string[];
    }
  | { readonly op: 'restore'; readonly snapshot: SnapshotFile };

export type Listener = (value: unknown, previous: unknown) => void;

interface Snapshot {
  readonly records: RecordMap;
  readonly version: number;
}

function cloneRecords(src: RecordMap): RecordMap {
  const out: RecordMap = new Map();
  for (const [id, rec] of src) out.set(id, { ...rec });
  return out;
}

/**
 * 响应式状态库（不依赖任何现成框架）。
 * - 读者只能看到已提交快照，事务中间态不可见；
 * - 提交 = 单次原子替换（this.committed = next）；
 * - 所有变更以操作日志记录，可回放重建。
 */
export class Store {
  private committed: Snapshot = { records: new Map(), version: 0 };
  /** 操作日志：按提交批次记录，保证回放时单调语义逐批一致。 */
  private readonly opLog: Op[][] = [];
  private readonly listeners = new Map<string, Set<Listener>>();

  private readonly registry: Registry;

  constructor(registry: Registry) {
    this.registry = registry;
  }

  /** 读取已提交快照中的值；未知节点抛错。 */
  get(id: string): unknown {
    const rec = this.committed.records.get(id);
    if (!rec) throw new RestoreError('UNKNOWN_NODE', `unknown node: ${id}`);
    return rec.value;
  }

  has(id: string): boolean {
    return this.committed.records.has(id);
  }

  get version(): number {
    return this.committed.version;
  }

  /** 测试/序列化用的内部视图（请勿修改返回值）。 */
  recordsView(): ReadonlyMap<string, NodeRecord> {
    return this.committed.records;
  }

  /** 订阅已提交值的变化；回调只在提交/恢复完整结束后触发。 */
  subscribe(id: string, listener: Listener): () => void {
    let set = this.listeners.get(id);
    if (!this.listeners.has(id)) {
      set = new Set();
      this.listeners.set(id, set);
    }
    set!.add(listener);
    return () => set!.delete(listener);
  }

  begin(): Transaction {
    return new Transaction(this);
  }

  /** 已记录的操作日志（按提交批次，可回放）。 */
  ops(): readonly OpBatch[] {
    return this.opLog.map((batch) => batch.slice());
  }

  /** 通过回放操作日志重建一个等价 Store。 */
  static replay(batches: readonly OpBatch[], registry: Registry): Store {
    const store = new Store(registry);
    for (const batch of batches) store.applyOps(batch);
    return store;
  }

  /**
   * 原子恢复：先在暂存区完成全部校验与求值，
   * 成功后一次性替换已提交快照；失败则 live 状态保持可读、不通知任何订阅者。
   */
  restore(file: unknown): void {
    const records = stageRestore(file, this.registry);
    const prev = this.committed;
    this.committed = { records, version: prev.version + 1 };
    this.opLog.push([{ op: 'restore', snapshot: file as SnapshotFile }]);
    this.notifyChanged(prev);
  }

  /** 应用一批操作：先在新副本上校验+求值，再原子发布。（内部入口，事务/回放使用） */
  applyOps(ops: readonly Op[]): void {
    const prev = this.committed;
    const records = cloneRecords(prev.records);
    const changed = new Set<string>();

    for (const op of ops) {
      switch (op.op) {
        case 'defineState': {
          if (records.has(op.id)) {
            throw new RestoreError('DUPLICATE_NODE', `node already exists: ${op.id}`);
          }
          records.set(op.id, { kind: 'state', id: op.id, value: op.value });
          changed.add(op.id);
          break;
        }
        case 'set': {
          const rec = records.get(op.id);
          if (!rec) throw new RestoreError('UNKNOWN_NODE', `unknown node: ${op.id}`);
          if (rec.kind !== 'state') {
            throw new RestoreError('NOT_A_STATE_NODE', `cannot set computed node: ${op.id}`);
          }
          if (!Object.is(rec.value, op.value)) {
            records.set(op.id, { ...rec, value: op.value });
            changed.add(op.id);
          }
          break;
        }
        case 'defineComputed': {
          if (records.has(op.id)) {
            throw new RestoreError('DUPLICATE_NODE', `node already exists: ${op.id}`);
          }
          const def = this.registry.get(op.defId, op.defVersion);
          if (!def) {
            if (this.registry.hasId(op.defId)) {
              throw new RestoreError(
                'DEFINITION_VERSION',
                `definition ${op.defId} registered versions [${this.registry.versionsOf(op.defId)}], requested ${op.defVersion}`,
              );
            }
            throw new RestoreError('UNKNOWN_DEFINITION', `unknown definition: ${op.defId}@${op.defVersion}`);
          }
          records.set(op.id, {
            kind: 'computed',
            id: op.id,
            defId: op.defId,
            defVersion: op.defVersion,
            deps: op.deps.slice(),
            value: undefined,
            dirty: true,
          });
          changed.add(op.id);
          break;
        }
        case 'restore': {
          // 暂存区已完成校验与求值，直接落位；不得再标脏，
          // 否则单调节点会按携带值重复累积。
          const staged = stageRestore(op.snapshot, this.registry);
          for (const [id, rec] of staged) records.set(id, rec);
          break;
        }
      }
    }

    checkMissingDeps(records);
    checkMonotonicCycles(records, this.registry);
    checkEvaluationCycles(records);
    markDirtyDownstream(records, changed);
    recomputeAll(records, prev.records, this.registry);

    this.committed = { records, version: prev.version + 1 };
    this.opLog.push(ops.slice());
    this.notifyChanged(prev);
  }

  private notifyChanged(prev: Snapshot): void {
    for (const [id, listeners] of this.listeners) {
      if (listeners.size === 0) continue;
      const oldValue = prev.records.get(id)?.value;
      const newValue = this.committed.records.get(id)?.value;
      if (Object.is(oldValue, newValue)) continue;
      for (const listener of [...listeners]) listener(newValue, oldValue);
    }
  }
}

export class Transaction {
  private readonly pending: Op[] = [];
  private done = false;

  private readonly store: Store;

  constructor(store: Store) {
    this.store = store;
  }

  defineState(id: string, value: unknown): this {
    this.pending.push({ op: 'defineState', id, value });
    return this;
  }

  set(id: string, value: unknown): this {
    this.pending.push({ op: 'set', id, value });
    return this;
  }

  defineComputed(id: string, def: ComputationDef, deps: readonly string[]): this {
    this.pending.push({
      op: 'defineComputed',
      id,
      defId: def.id,
      defVersion: def.version,
      deps: deps.slice(),
    });
    return this;
  }

  /** 原子提交：任一操作非法则整批不落盘。 */
  commit(): void {
    if (this.done) throw new Error('transaction already committed');
    this.done = true;
    this.store.applyOps(this.pending);
  }
}
