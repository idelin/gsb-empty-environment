/** Keyboard driver: translates keys into the same logical intents the
 *  mouse driver uses, so both produce identical operation traces.
 *
 *  Idle:     ArrowUp/ArrowDown move focus, Enter lifts the focused node.
 *  Dragging: ArrowUp/ArrowDown step the drop target through every valid
 *            location in visual order, ArrowLeft promotes a level,
 *            ArrowRight demotes under the previous sibling,
 *            Enter commits, Escape cancels. */
import type { Location, NodeId, Tree } from '../types.ts';
import { flatten } from '../tree.ts';
import { ListClient } from '../client.ts';

interface Candidate extends Location { row: number; depth: number }

export class KeyboardDriver {
  private client: ListClient;

  constructor(client: ListClient) {
    this.client = client;
  }

  key(k: 'ArrowUp' | 'ArrowDown' | 'ArrowLeft' | 'ArrowRight' | 'Enter' | 'Escape'): void {
    const m = this.client.machine;
    if (m.phase === 'awaiting') return;
    if (m.phase === 'idle') {
      const rows = flatten(this.client.tree);
      if (k === 'Enter' && m.ui.focusId) {
        this.client.startDrag(m.ui.focusId);
      } else if ((k === 'ArrowDown' || k === 'ArrowUp') && rows.length > 0) {
        const idx = rows.findIndex((r) => r.id === m.ui.focusId);
        const next = k === 'ArrowDown'
          ? Math.min(idx + 1, rows.length - 1)
          : Math.max(idx - 1, 0);
        const id = rows[next].id;
        m.ui = { focusId: id, selection: [id] };
      }
      return;
    }
    switch (k) {
      case 'Enter': this.client.commitDrag(); break;
      case 'Escape': this.client.cancelDrag(); break;
      case 'ArrowUp': this.step(-1); break;
      case 'ArrowDown': this.step(1); break;
      case 'ArrowLeft': this.promote(); break;
      case 'ArrowRight': this.demote(); break;
    }
  }

  /** All legal drop locations in visual order, computed on the detached
   *  tree (post-removal coordinates). */
  private candidates(): Candidate[] {
    const m = this.client.machine;
    const tree = m.detachedTree();
    const rows = flatten(tree);
    const depthOf = new Map<NodeId, number>(rows.map((r) => [r.id, r.depth]));
    const rowOf = new Map<NodeId, number>(rows.map((r, i) => [r.id, i]));
    const subtreeSize = (id: NodeId): number => {
      let n = 1;
      for (const c of tree.nodes[id].children) n += subtreeSize(c);
      return n;
    };
    const out: Candidate[] = [];
    const parents: NodeId[] = [tree.rootId, ...rows.map((r) => r.id)];
    for (const pid of parents) {
      const node = tree.nodes[pid];
      const depth = (pid === tree.rootId ? -1 : depthOf.get(pid)!) + 1;
      for (let i = 0; i <= node.children.length; i++) {
        const row = i < node.children.length
          ? rowOf.get(node.children[i])!
          : pid === tree.rootId ? rows.length : rowOf.get(pid)! + subtreeSize(pid);
        out.push({ parentId: pid, index: i, row, depth });
      }
    }
    out.sort((a, b) => a.row - b.row || a.depth - b.depth);
    return out;
  }

  private step(dir: 1 | -1): void {
    const m = this.client.machine;
    const target = m.currentTarget;
    if (!target) return;
    const all = this.candidates();
    const idx = all.findIndex((c) => c.parentId === target.parentId && c.index === target.index);
    if (idx === -1) return;
    const next = all[Math.max(0, Math.min(idx + dir, all.length - 1))];
    m.setTargetNormalized({ parentId: next.parentId, index: next.index });
  }

  private promote(): void {
    const m = this.client.machine;
    const target = m.currentTarget;
    if (!target) return;
    const tree = m.detachedTree();
    const parent = tree.nodes[target.parentId];
    if (parent.parentId === null) return; // already top level
    const grand = tree.nodes[parent.parentId];
    m.setTargetNormalized({
      parentId: grand.id,
      index: grand.children.indexOf(parent.id) + 1,
    });
  }

  private demote(): void {
    const m = this.client.machine;
    const target = m.currentTarget;
    if (!target || target.index === 0) return;
    const tree: Tree = m.detachedTree();
    const newParent = tree.nodes[target.parentId].children[target.index - 1];
    m.setTargetNormalized({
      parentId: newParent,
      index: tree.nodes[newParent].children.length,
    });
  }
}
