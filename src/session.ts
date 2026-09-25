import type { MoveOp, MoveSpec, NodeId, TraceEntry, TreeState } from './types.ts';
import { specsEqual } from './types.ts';
import { isValidMove } from './tree.ts';

export type InputKind = 'mouse' | 'keyboard';
export type SessionPhase = 'idle' | 'dragging' | 'awaiting-ack' | 'settled';

export interface SessionSnapshot {
  focusId: NodeId | null;
  selection: NodeId[];
}

/**
 * 鼠标/键盘共用的拖拽会话状态机。
 * 状态迁移：idle -> dragging -> awaiting-ack -> settled
 *                         dragging -> settled (cancel / terminate)
 * 预览只记录在会话上（currentSpec），绝不修改正式树。
 */
export class DragSession {
  readonly id: string;
  readonly inputKind: InputKind;
  phase: SessionPhase = 'idle';
  sourceId: NodeId | null = null;
  currentSpec: MoveSpec | null = null;
  invalidSpec: MoveSpec | null = null;
  beginVersion = 0;
  targetRevision = 0;
  snapshot: SessionSnapshot | null = null;
  private trace: TraceEntry[];

  constructor(id: string, inputKind: InputKind, trace: TraceEntry[]) {
    this.id = id;
    this.inputKind = inputKind;
    this.trace = trace;
  }

  begin(tree: TreeState, sourceId: NodeId, snapshot: SessionSnapshot): void {
    if (this.phase !== 'idle') throw new Error(`session ${this.id} already started`);
    if (!tree.nodes[sourceId]) throw new Error(`unknown source node: ${sourceId}`);
    this.sourceId = sourceId;
    this.beginVersion = tree.version;
    this.snapshot = snapshot;
    this.phase = 'dragging';
    this.trace.push({ kind: 'begin', sourceId });
  }

  /** 更新预览目标；非法目标（如自身后代）被拦截并记录为 invalidSpec */
  updateTarget(tree: TreeState, spec: MoveSpec): boolean {
    if (this.phase !== 'dragging') return false;
    if (!isValidMove(tree, spec)) {
      this.invalidSpec = spec;
      return false;
    }
    this.invalidSpec = null;
    if (this.currentSpec && specsEqual(this.currentSpec, spec)) return true;
    this.currentSpec = spec;
    this.targetRevision = tree.nodes[spec.targetId].revision;
    this.trace.push({ kind: 'preview', spec });
    return true;
  }

  /** 生成提交操作：携带操作ID、源版本（开始拖拽时的全树版本）与目标版本 */
  buildOp(nextOpId: () => string): MoveOp | null {
    if (this.phase !== 'dragging' || this.currentSpec === null || this.sourceId === null) return null;
    const op: MoveOp = {
      ...this.currentSpec,
      opId: nextOpId(),
      sourceVersion: this.beginVersion,
      targetRevision: this.targetRevision,
    };
    this.phase = 'awaiting-ack';
    this.trace.push({ kind: 'commit', op });
    return op;
  }

  /** 取消：返回快照以便恢复全部状态 */
  cancel(): SessionSnapshot | null {
    if (this.phase !== 'dragging') return null;
    this.phase = 'settled';
    this.trace.push({ kind: 'cancel' });
    return this.snapshot;
  }

  /** 外部失效（如目标在拖动中被删除）：安全终止 */
  terminate(reason: string): SessionSnapshot | null {
    if (this.phase !== 'dragging') return null;
    this.phase = 'settled';
    this.trace.push({ kind: 'terminate', reason });
    return this.snapshot;
  }

  settle(): void {
    this.phase = 'settled';
  }
}
