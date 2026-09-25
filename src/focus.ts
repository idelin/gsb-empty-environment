import type { NodeId, NodeTree } from "./tree.ts";
import type { InertRegistry } from "./inert.ts";

/**
 * 焦点管理器:焦点只允许落在当前可交互范围(顶层弹层或文档)内,
 * 且目标必须已挂载、可见、可聚焦、非 inert。
 */
export class FocusManager {
  active: NodeId | null = null;
  private tree: NodeTree;
  private inert: InertRegistry;
  private scopeRoot: () => NodeId;

  constructor(tree: NodeTree, inert: InertRegistry, scopeRoot: () => NodeId) {
    this.tree = tree;
    this.inert = inert;
    this.scopeRoot = scopeRoot;
  }

  isFocusable(id: NodeId): boolean {
    const node = this.tree.get(id);
    if (!node || !node.focusable || node.disabled) return false;
    if (!this.tree.isVisible(id)) return false;
    if (this.inert.isInert(id, this.tree)) return false;
    return this.tree.contains(this.scopeRoot(), id);
  }

  /** 焦点陷阱:范围外的聚焦请求被拒绝。 */
  focus(id: NodeId): boolean {
    if (!this.isFocusable(id)) return false;
    this.active = id;
    return true;
  }

  blur(): void {
    this.active = null;
  }

  /** 节点删除/inert 变化后调用,清理失效焦点。 */
  ensureActiveValid(): void {
    if (this.active !== null && !this.isFocusable(this.active)) this.active = null;
  }

  /** 当前范围内的可聚焦节点(文档序)。 */
  candidates(): NodeId[] {
    return this.tree.documentOrder().filter((id) => this.isFocusable(id));
  }

  focusNext(): NodeId | null {
    return this.cycle(1);
  }

  focusPrev(): NodeId | null {
    return this.cycle(-1);
  }

  /** Tab 循环被限制在范围内并回绕。 */
  private cycle(step: 1 | -1): NodeId | null {
    const cands = this.candidates();
    if (cands.length === 0) {
      this.active = null;
      return null;
    }
    const idx = this.active === null ? -1 : cands.indexOf(this.active);
    const next =
      idx === -1
        ? step === 1
          ? cands[0]
          : cands[cands.length - 1]
        : cands[(idx + step + cands.length) % cands.length];
    this.active = next;
    return next;
  }
}
