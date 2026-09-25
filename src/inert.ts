import type { NodeId, NodeTree } from "./tree.ts";

/**
 * inert 引用计数注册表。
 * 每次获取在某一子树根上 +1,释放 -1;计数沿树向下生效。
 * 嵌套弹层各自持有引用,内层释放不会解除外层留下的隔离。
 */
export class InertRegistry {
  private counts: Map<NodeId, number> = new Map();
  private handles: Map<number, NodeId> = new Map();
  private nextHandle = 1;

  acquire(root: NodeId): number {
    const handle = this.nextHandle++;
    this.handles.set(handle, root);
    this.counts.set(root, (this.counts.get(root) ?? 0) + 1);
    return handle;
  }

  /** 幂等释放:重复释放同一引用是 no-op。 */
  release(handle: number): void {
    const root = this.handles.get(handle);
    if (root === undefined) return;
    this.handles.delete(handle);
    const next = (this.counts.get(root) ?? 0) - 1;
    if (next <= 0) this.counts.delete(root);
    else this.counts.set(root, next);
  }

  releaseAll(handles: Iterable<number>): void {
    for (const handle of handles) this.release(handle);
  }

  /**
   * 节点子树被移除时调用:丢弃落在已消失节点上的计数。
   * inert 是元素的状态,元素消失状态随之消失;指向它的存根句柄
   * 之后释放时自然成为 no-op,引用计数保持平衡。
   */
  drop(removedIds: Iterable<NodeId>): void {
    for (const id of removedIds) this.counts.delete(id);
  }

  /** 自身或任一祖先计数 > 0 即为 inert。 */
  isInert(id: NodeId, tree: NodeTree): boolean {
    let cur: NodeId | null = id;
    while (cur !== null) {
      if (this.counts.has(cur)) return true;
      cur = tree.parent(cur);
    }
    return false;
  }

  /** 存活的引用总数(泄漏检查:全部关闭后必须为 0)。 */
  get totalRefs(): number {
    return this.handles.size;
  }

  snapshot(): Record<string, number> {
    const out: Record<string, number> = {};
    for (const key of [...this.counts.keys()].sort()) {
      out[key] = this.counts.get(key)!;
    }
    return out;
  }
}
