/** Client session: owns the local canonical mirror, the shared drag
 *  machine, pending ops and server-message handling.
 *
 *  Key invariants:
 *  - The canonical tree only ever changes via server results/broadcasts,
 *    never via drag previews.
 *  - A failed commit discards only the preview; independent edits the
 *    user made afterwards stay applied.
 *  - Retries reuse the same opId, so the server applies them at most once. */
import type { CommitOp, CommitResult, NodeId, ServerMessage, Tree, TreeNode } from './types.ts';
import { DragMachine } from './stateMachine.ts';
import { Server } from './server.ts';
import type { OpResult } from './server.ts';
import { applyMovePatch, insertNode, removeSubtree } from './tree.ts';

let clientSeq = 0;

/** A transport returns the server result, or null when the
 *  acknowledgement was lost (the op stays pending and can be retried). */
export type Transport = (op: CommitOp) => CommitResult | null;

export class ListClient {
  readonly id: string;
  readonly machine: DragMachine;
  version: number;
  transport: Transport;
  private pending = new Map<string, CommitOp>();
  private seen = new Set<string>();
  private opSeq = 0;
  private dragBaseVersion = 0;
  private server: Server;

  constructor(server: Server, id?: string) {
    this.server = server;
    this.id = id ?? `client-${++clientSeq}`;
    this.machine = new DragMachine(server.tree);
    this.version = server.version;
    this.transport = (op) => this.server.commit(this.id, op);
    this.server.register(this.id, (msg) => this.onMessage(msg));
  }

  get tree(): Tree { return this.machine.canonicalTree; }
  get pendingOpIds(): string[] { return [...this.pending.keys()]; }

  private nextOpId(): string { return `${this.id}-op-${++this.opSeq}`; }

  // ---- drag intents (shared by mouse and keyboard drivers) ----

  startDrag(nodeId: NodeId): boolean {
    const ok = this.machine.start(nodeId);
    if (ok) this.dragBaseVersion = this.version;
    return ok;
  }

  dragTo(loc: { parentId: NodeId; index: number }): boolean {
    return this.machine.setTarget(loc);
  }

  cancelDrag(): void {
    this.machine.cancel();
  }

  commitDrag(): CommitResult | null {
    const patch = this.machine.commit();
    if (!patch) return null;
    const op: CommitOp = { opId: this.nextOpId(), baseVersion: this.dragBaseVersion, patch };
    this.pending.set(op.opId, op);
    const res = this.transport(op);
    if (res) this.handleCommitResult(res);
    return res;
  }

  /** Resend every unacknowledged op. Safe: opIds make retries idempotent. */
  retryPending(): void {
    for (const [opId, op] of [...this.pending]) {
      const res = this.transport(op);
      if (res) this.handleCommitResult(res);
      if (!this.pending.has(opId)) continue;
    }
  }

  private handleCommitResult(res: CommitResult): void {
    if (!res.ok) {
      // Failure: drop the preview only. The canonical tree (including any
      // independent edits made after the drag started) is preserved.
      this.pending.delete(res.opId);
      this.machine.resolveCommit(false);
      return;
    }
    this.pending.delete(res.opId);
    if (!this.seen.has(res.opId)) {
      this.seen.add(res.opId);
      const next = applyMovePatch(this.tree, res.patch);
      this.version = res.version;
      this.machine.resolveCommit(true);
      this.machine.rebase(next);
    } else {
      this.machine.resolveCommit(true);
    }
  }

  // ---- independent edits (not part of a drag) ----

  insertNode(parentId: NodeId, index: number, node: TreeNode): OpResult {
    const res = this.server.insert(this.id, { opId: this.nextOpId(), node, parentId, index });
    if (res.ok && !this.seen.has(res.opId)) {
      this.seen.add(res.opId);
      const p = res.patch;
      if (p.kind === 'insert') {
        this.version = res.version;
        this.machine.rebase(insertNode(this.tree, p.node, p.parentId, p.index));
      }
    }
    return res;
  }

  removeNode(nodeId: NodeId): OpResult {
    const res = this.server.remove(this.id, { opId: this.nextOpId(), nodeId });
    if (res.ok && !this.seen.has(res.opId)) {
      this.seen.add(res.opId);
      const p = res.patch;
      if (p.kind === 'remove') {
        this.version = res.version;
        this.machine.rebase(removeSubtree(this.tree, p.nodeId));
      }
    }
    return res;
  }

  // ---- server broadcasts ----

  private onMessage(msg: ServerMessage): void {
    if (this.seen.has(msg.opId)) return;
    this.seen.add(msg.opId);
    let next: Tree;
    if (msg.type === 'reorder') next = applyMovePatch(this.tree, msg.patch);
    else if (msg.type === 'insert') next = insertNode(this.tree, msg.patch.node, msg.patch.parentId, msg.patch.index);
    else next = removeSubtree(this.tree, msg.patch.nodeId);
    this.version = msg.version;
    // Rebase validates any in-flight drag and terminates it safely when
    // the dragged node or its target disappeared.
    this.machine.rebase(next);
  }
}
