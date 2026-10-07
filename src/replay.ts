import { collectRefs, rewriteRefs } from './rewrite.ts';
import { MemoryStorage, type Storage } from './storage.ts';

export type OpStatus = 'pending' | 'done' | 'failed' | 'blocked';

export interface Operation {
  /** 客户端操作 id，队列内唯一。 */
  id: string;
  kind: string;
  /** JSON 树，字符串叶子可引用其他操作创建的临时对象 id。 */
  payload: unknown;
  /** 本操作成功后创建的临时对象 id。 */
  creates?: string;
  /** 显式依赖的操作 id（引用推断之外的补充）。 */
  dependsOn?: readonly string[];
}

export interface Receipt {
  opId: string;
  ok: boolean;
  /** 临时 id -> 服务器 id 的映射。 */
  serverIds?: Record<string, string>;
  error?: string;
}

export type Transport = (op: Operation) => Promise<Receipt>;

export interface FlushReport {
  /** 本次 flush 实际发送的操作。 */
  sent: string[];
  done: string[];
  failed: string[];
  blocked: string[];
  pending: string[];
}

interface StoredOp extends Operation {
  status: OpStatus;
}

interface Snapshot {
  version: 1;
  ops: StoredOp[];
  idMap: Record<string, string>;
}

/**
 * 离线编辑重放引擎：
 * - 按依赖拓扑序执行（Kahn，同层按入队顺序，确定性）；
 * - 失败仅阻断其传递后继，独立操作继续；
 * - 服务器 id 回执递归改写给未发送操作的 payload；
 * - 回执幂等，重复回执安全丢弃；
 * - 每次状态变更持久化，重启后从快照恢复。
 */
export class ReplayEngine {
  private ops: StoredOp[] = [];
  private idMap: Record<string, string> = {};

  private readonly transport: Transport;
  private readonly storage: Storage;

  constructor(transport: Transport, storage: Storage = new MemoryStorage()) {
    this.transport = transport;
    this.storage = storage;
    const raw = storage.load();
    if (raw !== null) {
      const snapshot = JSON.parse(raw) as Snapshot;
      this.ops = snapshot.ops;
      this.idMap = snapshot.idMap;
    }
  }

  enqueue(op: Operation): void {
    if (this.ops.some((existing) => existing.id === op.id)) {
      throw new Error(`duplicate op id: ${op.id}`);
    }
    this.ops.push({
      ...op,
      payload: rewriteRefs(op.payload, this.idMap),
      status: 'pending',
    });
    this.persist();
  }

  status(opId: string): OpStatus | undefined {
    return this.ops.find((op) => op.id === opId)?.status;
  }

  idMappings(): Readonly<Record<string, string>> {
    return { ...this.idMap };
  }

  list(): ReadonlyArray<Readonly<StoredOp>> {
    return this.ops.map((op) => ({ ...op }));
  }

  /** 执行所有可执行的 pending 操作，直到没有可推进的为止。 */
  async flush(): Promise<FlushReport> {
    const sent: string[] = [];
    for (;;) {
      this.propagateBlocked();
      const ready = this.ops.filter(
        (op) => op.status === 'pending' && this.depsSatisfied(op),
      );
      if (ready.length === 0) break;
      for (const op of ready) {
        sent.push(op.id);
        let receipt: Receipt;
        try {
          receipt = await this.transport(this.wire(op));
        } catch (err) {
          receipt = { opId: op.id, ok: false, error: String(err) };
        }
        this.applyReceipt({ ...receipt, opId: op.id });
      }
    }
    return {
      sent,
      done: this.byStatus('done'),
      failed: this.byStatus('failed'),
      blocked: this.byStatus('blocked'),
      pending: this.byStatus('pending'),
    };
  }

  /**
   * 应用一条服务器回执。幂等：对不存在或已终态的操作返回 false，
   * 不产生任何副作用（重复回执、重启后服务器重投均安全）。
   */
  applyReceipt(receipt: Receipt): boolean {
    const op = this.ops.find((candidate) => candidate.id === receipt.opId);
    if (!op || op.status !== 'pending') return false;
    if (!receipt.ok) {
      op.status = 'failed';
      this.persist();
      return true;
    }
    op.status = 'done';
    const fresh: Record<string, string> = {};
    for (const [tempId, serverId] of Object.entries(receipt.serverIds ?? {})) {
      if (this.idMap[tempId] === undefined) {
        this.idMap[tempId] = serverId;
        fresh[tempId] = serverId;
      }
    }
    if (Object.keys(fresh).length > 0) {
      for (const pending of this.ops) {
        if (pending.status === 'pending') {
          pending.payload = rewriteRefs(pending.payload, fresh);
        }
      }
    }
    this.persist();
    return true;
  }

  /** 将 failed/blocked 操作重置为 pending 以便重试。 */
  requeue(opId: string): boolean {
    const op = this.ops.find((candidate) => candidate.id === opId);
    if (!op || (op.status !== 'failed' && op.status !== 'blocked')) return false;
    op.status = 'pending';
    this.persist();
    return true;
  }

  requeueAll(): number {
    let count = 0;
    for (const op of this.ops) {
      if (op.status === 'failed' || op.status === 'blocked') {
        op.status = 'pending';
        count += 1;
      }
    }
    if (count > 0) this.persist();
    return count;
  }

  private wire(op: StoredOp): Operation {
    return {
      id: op.id,
      kind: op.kind,
      payload: op.payload,
      creates: op.creates,
      dependsOn: op.dependsOn,
    };
  }

  private byStatus(status: OpStatus): string[] {
    return this.ops.filter((op) => op.status === status).map((op) => op.id);
  }

  /** 临时 id -> 创建它的操作 id。 */
  private producers(): Map<string, string> {
    const map = new Map<string, string>();
    for (const op of this.ops) {
      if (op.creates !== undefined) map.set(op.creates, op.id);
    }
    return map;
  }

  /** 操作依赖：显式 dependsOn ∪ payload 引用的临时 id 的创建者。 */
  private dependenciesOf(op: StoredOp): Set<string> {
    const deps = new Set<string>();
    const known = new Set(this.ops.map((candidate) => candidate.id));
    for (const dep of op.dependsOn ?? []) {
      if (known.has(dep) && dep !== op.id) deps.add(dep);
    }
    const producers = this.producers();
    for (const ref of collectRefs(op.payload)) {
      const producer = producers.get(ref);
      if (producer !== undefined && producer !== op.id) deps.add(producer);
    }
    return deps;
  }

  private depsSatisfied(op: StoredOp): boolean {
    for (const dep of this.dependenciesOf(op)) {
      const target = this.ops.find((candidate) => candidate.id === dep);
      if (target !== undefined && target.status !== 'done') return false;
    }
    return true;
  }

  /** 失败/阻断沿依赖边传递：任何依赖 failed 或 blocked 的 pending 操作变为 blocked。 */
  private propagateBlocked(): void {
    let changed = true;
    while (changed) {
      changed = false;
      for (const op of this.ops) {
        if (op.status !== 'pending') continue;
        for (const dep of this.dependenciesOf(op)) {
          const target = this.ops.find((candidate) => candidate.id === dep);
          if (target && (target.status === 'failed' || target.status === 'blocked')) {
            op.status = 'blocked';
            changed = true;
            break;
          }
        }
      }
    }
  }

  private persist(): void {
    const snapshot: Snapshot = { version: 1, ops: this.ops, idMap: this.idMap };
    this.storage.save(JSON.stringify(snapshot));
  }
}
