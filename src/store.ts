import { compare, type VectorClock } from './version.ts';
import type { Batch, MergeRule, Op, Scalar, Schema } from './ops.ts';
import { ruleFor } from './ops.ts';

export type ApplyOrigin = 'local' | 'remote' | 'replay';

export interface AppliedEvent {
  op: Op;
  origin: ApplyOrigin;
}

interface LwwEntry {
  value: Scalar;
  lamport: number;
  replicaId: string;
}

interface MvEntry {
  opId: string;
  value: Scalar;
  clock: VectorClock;
}

interface FieldState {
  lww?: LwwEntry;
  mv?: Map<string, MvEntry>;
}

export type ResolvedField =
  | { kind: 'value'; value: Scalar }
  | { kind: 'conflict'; values: Scalar[] }
  | { kind: 'absent' };

/**
 * 副本存储：只保存已提交状态。
 * - apply 以事务批次为原子单位，中途不存在对外可见的中间态；
 * - appliedOpIds 保证任何重复投递 / 重放都不会二次应用；
 * - 监听器（副作用）仅在操作首次应用时触发。
 */
export class Store {
  private fields = new Map<string, FieldState>();
  private appliedOpIds = new Set<string>();
  private log: Batch[] = [];
  private listeners: ((e: AppliedEvent) => void)[] = [];
  private readonly schema: Schema;

  constructor(schema: Schema) {
    this.schema = schema;
  }

  onApply(listener: (e: AppliedEvent) => void): void {
    this.listeners.push(listener);
  }

  /** 已应用的操作数（去重后）。 */
  get appliedCount(): number {
    return this.appliedOpIds.size;
  }

  /** 应用一个事务批次。返回本次真正新应用的操作（重复操作被跳过）。 */
  apply(batch: Batch, origin: ApplyOrigin, opts: { silent?: boolean } = {}): Op[] {
    const fresh: Op[] = [];
    for (const op of batch.ops) {
      if (this.appliedOpIds.has(op.opId)) continue; // 幂等：重复消息 / 重放不双应用
      this.appliedOpIds.add(op.opId);
      this.applyOp(op);
      fresh.push(op);
    }
    if (fresh.length > 0) this.log.push(batch);
    if (!opts.silent) {
      for (const op of fresh) {
        for (const l of this.listeners) l({ op, origin });
      }
    }
    return fresh;
  }

  private applyOp(op: Op): void {
    const rule = ruleFor(this.schema, op.field);
    if (rule.kind === 'lww') this.applyLww(op);
    else this.applyMv(op);
  }

  private applyLww(op: Op): void {
    const state = this.stateFor(op.field);
    const cur = state.lww;
    // 显式决胜规则：(lamport, replicaId) 字典序，大者胜 —— 全序，结果确定。
    if (!cur || op.lamport > cur.lamport || (op.lamport === cur.lamport && op.replicaId > cur.replicaId)) {
      state.lww = { value: op.value, lamport: op.lamport, replicaId: op.replicaId };
    }
  }

  private applyMv(op: Op): void {
    const state = this.stateFor(op.field);
    const entries = (state.mv ??= new Map());
    for (const [id, e] of entries) {
      const rel = compare(e.clock, op.clock);
      if (rel === 'dominates' || rel === 'equal') return; // 旧写已覆盖此操作：过期，忽略
    }
    for (const [id, e] of entries) {
      const rel = compare(op.clock, e.clock);
      if (rel === 'dominates' || rel === 'equal') entries.delete(id); // 新写覆盖旧写
    }
    entries.set(op.opId, { opId: op.opId, value: op.value, clock: op.clock });
  }

  private stateFor(field: string): FieldState {
    let s = this.fields.get(field);
    if (!s) {
      s = {};
      this.fields.set(field, s);
    }
    return s;
  }

  /** 解析单个字段的对外可见值。 */
  resolve(field: string): ResolvedField {
    const state = this.fields.get(field);
    if (!state) return { kind: 'absent' };
    const rule = ruleFor(this.schema, field);
    if (rule.kind === 'lww') {
      if (!state.lww || state.lww.value === null) return { kind: 'absent' };
      return { kind: 'value', value: state.lww.value };
    }
    const entries = [...(state.mv?.values() ?? [])].filter((e) => e.value !== null);
    if (entries.length === 0) return { kind: 'absent' };
    if (entries.length === 1) return { kind: 'value', value: entries[0]!.value };
    const values = entries.map((e) => e.value);
    if (rule.resolver === 'preserve') return { kind: 'conflict', values };
    const sorted = [...values].sort();
    return { kind: 'value', value: rule.resolver === 'max' ? sorted[sorted.length - 1]! : sorted[0]! };
  }

  /** 已提交状态的不可变快照：读者只能看到事务边界后的完整状态。 */
  snapshot(): Readonly<Record<string, unknown>> {
    const out: Record<string, unknown> = {};
    for (const field of this.fields.keys()) {
      const r = this.resolve(field);
      if (r.kind === 'value') out[field] = r.value;
      else if (r.kind === 'conflict') out[field] = Object.freeze([...r.values]);
    }
    return Object.freeze(out);
  }

  /** 字段当前的并发冲突值（仅多值寄存器）。 */
  conflicts(field: string): Scalar[] {
    const r = this.resolve(field);
    return r.kind === 'conflict' ? r.values : [];
  }

  /** 已应用批次的日志，可用于重放重建。 */
  opLog(): readonly Batch[] {
    return this.log;
  }

  /** 从日志重放，重建等价状态（静默，不触发副作用）。 */
  static replay(schema: Schema, batches: readonly Batch[]): Store {
    const store = new Store(schema);
    for (const b of batches) store.apply(b, 'replay', { silent: true });
    return store;
  }
}
