import type { ApplyResult, MoveOp, MoveSpec, NodeId, Row, TraceEntry, TreeState } from './types.ts';
import { listRows } from './tree.ts';
import { computePreviewRows } from './projection.ts';
import { DragSession } from './session.ts';
import type { InputKind, SessionSnapshot } from './session.ts';
import type { Server } from './server.ts';

export type Transport = (op: MoveOp) => ApplyResult | null;

/**
 * 客户端控制器：持有正式树、焦点与选择；
 * 预览阶段绝不修改正式树，只有服务器回包（applied / reorder / 其他编辑）才替换树。
 */
export class ClientController {
  tree: TreeState;
  focusId: NodeId | null = null;
  selection = new Set<NodeId>();
  readonly trace: TraceEntry[] = [];
  readonly server: Server;
  private sessions = new Map<string, DragSession>();
  private sessionCounter = 0;
  private opCounter = 0;
  private transport: Transport;

  constructor(initial: TreeState, server: Server, transport?: Transport) {
    this.tree = initial;
    this.server = server;
    this.transport = transport ?? ((op) => server.applyMove(op));
  }

  rows(): Row[] {
    return listRows(this.tree);
  }

  setFocus(id: NodeId | null): void {
    this.focusId = id;
  }

  select(ids: Iterable<NodeId>): void {
    this.selection = new Set(ids);
  }

  beginSession(inputKind: InputKind, sourceId: NodeId): DragSession {
    const session = new DragSession(`session-${++this.sessionCounter}`, inputKind, this.trace);
    session.begin(this.tree, sourceId, { focusId: this.focusId, selection: [...this.selection] });
    this.sessions.set(session.id, session);
    this.focusId = sourceId;
    this.selection = new Set([sourceId]);
    return session;
  }

  hover(session: DragSession, spec: MoveSpec): boolean {
    return session.updateTarget(this.tree, spec);
  }

  commit(session: DragSession): ApplyResult | null {
    const op = session.buildOp(() => `op-${++this.opCounter}`);
    if (!op) return null;
    const result = this.sendWithRetry(op);
    if (result.status === 'applied') {
      this.tree = result.tree;
      this.focusId = op.sourceId;
      this.selection = new Set([op.sourceId]);
    } else {
      // 提交失败：正式树保持不动，随后发生的独立编辑自然保留
      this.focusId = this.tree.nodes[op.sourceId] ? op.sourceId : (this.rows()[0]?.id ?? null);
      this.selection = this.focusId !== null ? new Set([this.focusId]) : new Set();
    }
    session.settle();
    this.sessions.delete(session.id);
    return result;
  }

  cancel(session: DragSession): void {
    const snap = session.cancel();
    if (snap) this.restoreSnapshot(snap);
    this.sessions.delete(session.id);
  }

  /** 服务器回包（重排/删除/编辑）：替换正式树并重新校验进行中的会话 */
  applyServerTree(tree: TreeState): void {
    this.tree = tree;
    for (const session of [...this.sessions.values()]) {
      if (session.phase !== 'dragging') continue;
      if (session.sourceId !== null && !this.tree.nodes[session.sourceId]) {
        this.terminate(session, 'source-removed');
      } else if (session.currentSpec && !this.tree.nodes[session.currentSpec.targetId]) {
        this.terminate(session, 'target-removed');
      }
    }
  }

  rename(id: NodeId, label: string): void {
    this.applyServerTree(this.server.rename(id, label));
  }

  reorder(parentId: NodeId | null, orderedChildIds: NodeId[]): void {
    this.applyServerTree(this.server.reorder(parentId, orderedChildIds));
  }

  remove(id: NodeId): void {
    this.applyServerTree(this.server.remove(id));
  }

  /** 移动预览：在克隆上投影，正式树不变 */
  previewRows(): Row[] {
    for (const session of this.sessions.values()) {
      if (session.phase === 'dragging' && session.currentSpec) {
        return computePreviewRows(this.tree, session.currentSpec);
      }
    }
    return listRows(this.tree);
  }

  activeSessionCount(): number {
    let count = 0;
    for (const session of this.sessions.values()) {
      if (session.phase === 'dragging') count += 1;
    }
    return count;
  }

  private terminate(session: DragSession, reason: string): void {
    const snap = session.terminate(reason);
    if (snap) this.restoreSnapshot(snap);
    if (this.focusId === null || !this.tree.nodes[this.focusId]) {
      this.focusId = this.rows()[0]?.id ?? null;
    }
    this.sessions.delete(session.id);
  }

  private restoreSnapshot(snap: SessionSnapshot): void {
    if (snap.focusId === null) {
      this.focusId = null;
    } else {
      this.focusId = this.tree.nodes[snap.focusId] ? snap.focusId : (this.rows()[0]?.id ?? null);
    }
    this.selection = new Set([...snap.selection].filter((id) => this.tree.nodes[id]));
  }

  /** 失败重试使用同一操作ID，服务器据此幂等去重 */
  private sendWithRetry(op: MoveOp, attempts = 3): ApplyResult {
    for (let i = 0; i < attempts; i++) {
      const result = this.transport(op);
      if (result) return result;
    }
    return { status: 'rejected', opId: op.opId, reason: 'transport-failure', version: this.tree.version };
  }
}
