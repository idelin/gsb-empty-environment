/**
 * 节点树模型：DOM 式结构，由夹具直接给出（不走浏览器解析/布局）。
 *
 * 变更以“依赖键”形式记录，供增量重算使用：
 *   attr:<key>      属性变化（含 id 变化）
 *   role:<key>      角色变化
 *   text:<key>      文本变化
 *   hidden:<key>    隐藏状态变化
 *   children:<key>  子结构变化
 *   idref:<id>      某 id 的解析结果可能变化（节点增删 / id 改名）
 *   labelfor        任意 for 属性或结构变化（label 关联）
 */

export interface NodeSpec {
  key: string;
  id?: string;
  role?: string;
  attrs?: Record<string, string>;
  text?: string;
  hidden?: boolean;
  children?: NodeSpec[];
}

export interface TreeNode {
  key: string;
  id: string | null;
  role: string | null;
  attrs: Record<string, string>;
  text: string;
  hidden: boolean;
  parent: TreeNode | null;
  children: TreeNode[];
}

export interface DrainedChanges {
  keys: Set<string>;
  removed: Set<string>;
}

export class NodeTree {
  version = 0;
  readonly roots: TreeNode[] = [];
  private nodes = new Map<string, TreeNode>();
  private order: TreeNode[] = [];
  private pendingKeys = new Set<string>();
  private pendingRemoved = new Set<string>();

  constructor(specs: NodeSpec[]) {
    for (const spec of specs) this.roots.push(this.build(spec, null));
    this.rebuildOrder();
  }

  private build(spec: NodeSpec, parent: TreeNode | null): TreeNode {
    if (this.nodes.has(spec.key)) {
      throw new Error(`duplicate node key: ${spec.key}`);
    }
    const node: TreeNode = {
      key: spec.key,
      id: spec.id ?? null,
      role: spec.role ?? null,
      attrs: { ...(spec.attrs ?? {}) },
      text: spec.text ?? '',
      hidden: spec.hidden ?? false,
      parent,
      children: [],
    };
    this.nodes.set(node.key, node);
    for (const child of spec.children ?? []) {
      node.children.push(this.build(child, node));
    }
    return node;
  }

  private rebuildOrder(): void {
    this.order = [];
    const walk = (node: TreeNode): void => {
      this.order.push(node);
      for (const child of node.children) walk(child);
    };
    for (const root of this.roots) walk(root);
  }

  private touch(keys: string[]): void {
    for (const key of keys) this.pendingKeys.add(key);
    this.version++;
  }

  get(key: string): TreeNode | undefined {
    return this.nodes.get(key);
  }

  keys(): string[] {
    return this.order.map((node) => node.key);
  }

  compareKeys(a: string, b: string): number {
    return this.keys().indexOf(a) - this.keys().indexOf(b);
  }

  /** 按文档顺序解析 id，重复 id 时取第一个。 */
  byId(id: string): TreeNode | undefined {
    return this.order.find((node) => node.id === id);
  }

  /** 按文档顺序查找首个 attrs[name] === value 的节点（用于 label[for]）。 */
  findByAttr(name: string, value: string): TreeNode | undefined {
    return this.order.find((node) => node.attrs[name] === value);
  }

  setAttribute(key: string, name: string, value: string): void {
    const node = this.mustGet(key);
    node.attrs[name] = value;
    const keys = [`attr:${key}`];
    if (name === 'for') keys.push('labelfor');
    this.touch(keys);
  }

  removeAttribute(key: string, name: string): void {
    const node = this.mustGet(key);
    delete node.attrs[name];
    const keys = [`attr:${key}`];
    if (name === 'for') keys.push('labelfor');
    this.touch(keys);
  }

  setId(key: string, id: string | null): void {
    const node = this.mustGet(key);
    const old = node.id;
    node.id = id;
    const keys = [`attr:${key}`];
    if (old !== null) keys.push(`idref:${old}`);
    if (id !== null) keys.push(`idref:${id}`);
    this.touch(keys);
  }

  setRole(key: string, role: string | null): void {
    this.mustGet(key).role = role;
    this.touch([`role:${key}`]);
  }

  setText(key: string, text: string): void {
    this.mustGet(key).text = text;
    this.touch([`text:${key}`]);
  }

  setHidden(key: string, hidden: boolean): void {
    this.mustGet(key).hidden = hidden;
    this.touch([`hidden:${key}`]);
  }

  appendChild(parentKey: string, spec: NodeSpec): TreeNode {
    const parent = this.mustGet(parentKey);
    const node = this.build(spec, parent);
    parent.children.push(node);
    this.rebuildOrder();
    const keys = [`children:${parentKey}`, 'labelfor'];
    this.collectIdrefs(node, keys);
    this.touch(keys);
    return node;
  }

  removeNode(key: string): void {
    const node = this.mustGet(key);
    const keys: string[] = ['labelfor'];
    if (node.parent) {
      keys.push(`children:${node.parent.key}`);
      node.parent.children = node.parent.children.filter((c) => c !== node);
    } else {
      this.roots.splice(this.roots.indexOf(node), 1);
    }
    this.collectIdrefs(node, keys);
    const drop = (n: TreeNode): void => {
      this.nodes.delete(n.key);
      this.pendingRemoved.add(n.key);
      for (const child of n.children) drop(child);
    };
    drop(node);
    this.rebuildOrder();
    this.touch(keys);
  }

  private collectIdrefs(node: TreeNode, keys: string[]): void {
    if (node.id !== null) keys.push(`idref:${node.id}`);
    for (const child of node.children) this.collectIdrefs(child, keys);
  }

  /** 取出并清空自上次以来累积的变更（供增量引擎消费）。 */
  drainChanges(): DrainedChanges {
    const keys = this.pendingKeys;
    const removed = this.pendingRemoved;
    this.pendingKeys = new Set();
    this.pendingRemoved = new Set();
    return { keys, removed };
  }

  private mustGet(key: string): TreeNode {
    const node = this.nodes.get(key);
    if (!node) throw new Error(`unknown node key: ${key}`);
    return node;
  }
}
