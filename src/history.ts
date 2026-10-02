/**
 * 时间旅行控制器：
 * - 每次状态提交后记录全局所有 Signal 的不可变快照（structuredClone 深拷贝）
 * - 快照组织为树：Undo 后再产生新修改会生成分支
 * - Undo / Redo / jumpTo 恢复快照，并通过响应式通知自动驱动下游重渲染
 */

import { batch, onCommit, __listSignals, type Unsubscribe } from './reactive.ts';

export interface Snapshot {
  readonly id: number;
  readonly parentId: number | null;
  readonly childIds: readonly number[];
  readonly label: string;
  readonly createdAt: number;
  /** signalId -> 快照值（深拷贝，冻结） */
  readonly values: ReadonlyMap<number, unknown>;
}

interface SnapshotNode {
  id: number;
  parentId: number | null;
  childIds: number[];
  /** Undo 时记录来路，Redo 优先沿原路前进 */
  preferredChildId: number | null;
  label: string;
  createdAt: number;
  values: Map<number, unknown>;
}

export class TimeTravelController {
  private readonly nodes = new Map<number, SnapshotNode>();
  private currentId = 0;
  private nextId = 1;
  private restoring = false;
  private readonly disposeCommit: Unsubscribe;

  constructor() {
    this.nodes.set(0, this.capture(0, null, 'initial'));
    this.disposeCommit = onCommit(() => {
      if (!this.restoring) this.record();
    });
  }

  /** 当前指针所在的快照 */
  get current(): Snapshot {
    return this.toSnapshot(this.node(this.currentId));
  }

  get canUndo(): boolean {
    return this.node(this.currentId).parentId !== null;
  }

  get canRedo(): boolean {
    return this.node(this.currentId).childIds.length > 0;
  }

  /** 全部快照（按 id 升序，含各分支） */
  snapshots(): Snapshot[] {
    return [...this.nodes.values()]
      .sort((a, b) => a.id - b.id)
      .map((n) => this.toSnapshot(n));
  }

  /** 回退 steps 步，返回落点快照；步数不足时停在根 */
  undo(steps = 1): Snapshot {
    for (let i = 0; i < steps; i++) {
      const node = this.node(this.currentId);
      if (node.parentId === null) break;
      const parent = this.node(node.parentId);
      parent.preferredChildId = node.id;
      this.currentId = parent.id;
    }
    return this.applyCurrent();
  }

  /** 前进 steps 步（优先沿 Undo 来路），返回落点快照 */
  redo(steps = 1): Snapshot {
    for (let i = 0; i < steps; i++) {
      const node = this.node(this.currentId);
      const next =
        node.preferredChildId ?? node.childIds[node.childIds.length - 1];
      if (next === undefined || next === null) break;
      this.currentId = next;
    }
    return this.applyCurrent();
  }

  /** 跳转至任意历史快照 */
  jumpTo(snapshotId: number): Snapshot {
    if (!this.nodes.has(snapshotId)) {
      throw new RangeError(`unknown snapshot id: ${snapshotId}`);
    }
    this.currentId = snapshotId;
    return this.applyCurrent();
  }

  /** 断开与响应式核心的连接（不再记录新快照） */
  destroy(): void {
    this.disposeCommit();
  }

  private record(label = `change #${this.nextId}`): void {
    const node = this.capture(this.nextId++, this.currentId, label);
    this.node(this.currentId).childIds.push(node.id);
    this.nodes.set(node.id, node);
    this.currentId = node.id;
  }

  private capture(id: number, parentId: number | null, label: string): SnapshotNode {
    const values = new Map<number, unknown>();
    for (const handle of __listSignals()) {
      values.set(handle.id, structuredClone(handle.peek()));
    }
    return {
      id,
      parentId,
      childIds: [],
      preferredChildId: null,
      label,
      createdAt: Date.now(),
      values,
    };
  }

  private applyCurrent(): Snapshot {
    const node = this.node(this.currentId);
    this.restoring = true;
    try {
      // 单事务恢复：所有 Signal 一次性写回，下游只重渲染一次
      batch(() => {
        for (const handle of __listSignals()) {
          if (node.values.has(handle.id)) {
            handle.set(structuredClone(node.values.get(handle.id)));
          }
        }
      });
    } finally {
      this.restoring = false;
    }
    return this.toSnapshot(node);
  }

  private node(id: number): SnapshotNode {
    const found = this.nodes.get(id);
    if (!found) throw new RangeError(`unknown snapshot id: ${id}`);
    return found;
  }

  private toSnapshot(node: SnapshotNode): Snapshot {
    return Object.freeze({
      id: node.id,
      parentId: node.parentId,
      childIds: Object.freeze([...node.childIds]),
      label: node.label,
      createdAt: node.createdAt,
      values: new Map(node.values),
    });
  }
}
