/** 节点树:由夹具给出的 UI 结构,支持挂载/卸载与文档序遍历。 */
export type NodeId = string;

export interface UINode {
  id: NodeId;
  parent: NodeId | null;
  children: NodeId[];
  text: string;
  focusable: boolean;
  disabled: boolean;
  hidden: boolean;
  autofocus: boolean;
}

export interface NodeSpec {
  id: NodeId;
  text?: string;
  focusable?: boolean;
  disabled?: boolean;
  hidden?: boolean;
  autofocus?: boolean;
  children?: NodeSpec[];
}

type MountListener = (rootId: NodeId) => void;
type RemoveListener = (removed: NodeId[]) => void;

export class NodeTree {
  readonly rootId: NodeId;
  private nodes: Map<NodeId, UINode> = new Map();
  private mountListeners: MountListener[] = [];
  private removeListeners: RemoveListener[] = [];

  constructor(root: NodeSpec) {
    this.rootId = root.id;
    this.mountInto(null, root);
  }

  onMount(fn: MountListener): void {
    this.mountListeners.push(fn);
  }

  onRemove(fn: RemoveListener): void {
    this.removeListeners.push(fn);
  }

  private mountInto(parent: NodeId | null, spec: NodeSpec): NodeId {
    if (this.nodes.has(spec.id)) throw new Error(`duplicate node id: ${spec.id}`);
    const node: UINode = {
      id: spec.id,
      parent,
      children: [],
      text: spec.text ?? "",
      focusable: spec.focusable ?? false,
      disabled: spec.disabled ?? false,
      hidden: spec.hidden ?? false,
      autofocus: spec.autofocus ?? false,
    };
    this.nodes.set(node.id, node);
    if (parent !== null) this.mustGet(parent).children.push(node.id);
    for (const child of spec.children ?? []) this.mountInto(node.id, child);
    return node.id;
  }

  /** 挂载子树(可模拟异步挂载),返回子树根 id。 */
  mount(parent: NodeId, spec: NodeSpec): NodeId {
    const id = this.mountInto(parent, spec);
    for (const fn of this.mountListeners) fn(id);
    return id;
  }

  /** 卸载子树(根节点不可移除)。 */
  remove(id: NodeId): boolean {
    const node = this.nodes.get(id);
    if (!node || node.parent === null) return false;
    const removed: NodeId[] = [];
    const collect = (nid: NodeId): void => {
      removed.push(nid);
      for (const child of this.mustGet(nid).children) collect(child);
    };
    collect(id);
    const siblings = this.mustGet(node.parent).children;
    siblings.splice(siblings.indexOf(id), 1);
    for (const nid of removed) this.nodes.delete(nid);
    for (const fn of this.removeListeners) fn(removed);
    return true;
  }

  has(id: NodeId): boolean {
    return this.nodes.has(id);
  }

  get(id: NodeId): UINode | undefined {
    return this.nodes.get(id);
  }

  private mustGet(id: NodeId): UINode {
    const node = this.nodes.get(id);
    if (!node) throw new Error(`unknown node: ${id}`);
    return node;
  }

  parent(id: NodeId): NodeId | null {
    return this.nodes.get(id)?.parent ?? null;
  }

  /** 深度优先文档序。 */
  documentOrder(): NodeId[] {
    const order: NodeId[] = [];
    const visit = (id: NodeId): void => {
      order.push(id);
      for (const child of this.mustGet(id).children) visit(child);
    };
    visit(this.rootId);
    return order;
  }

  /** 包含关系(含自身)。 */
  contains(ancestor: NodeId, id: NodeId): boolean {
    let cur: NodeId | null = id;
    while (cur !== null) {
      if (cur === ancestor) return true;
      cur = this.parent(cur);
    }
    return false;
  }

  /** 自身及祖先均无 hidden。 */
  isVisible(id: NodeId): boolean {
    let cur: NodeId | null = id;
    while (cur !== null) {
      const node = this.nodes.get(cur);
      if (!node) return false;
      if (node.hidden) return false;
      cur = node.parent;
    }
    return true;
  }

  /** 顶层区域 = 根节点的直接子树(页面区、各弹层根)。 */
  topLevelRegions(): NodeId[] {
    return [...this.mustGet(this.rootId).children];
  }
}
