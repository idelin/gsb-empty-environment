import { MoveValidationError, NotFoundError, TreeError } from "./errors";
import { splitPath } from "./pathutil";

export interface TreeNode {
  readonly id: string;
  name: string;
  parentId: string | null;
}

export class Tree {
  readonly rootId = "root";
  private readonly nodes = new Map<string, TreeNode>();
  private readonly children = new Map<string, Set<string>>();
  private counter = 0;

  constructor() {
    this.nodes.set(this.rootId, { id: this.rootId, name: "", parentId: null });
    this.children.set(this.rootId, new Set());
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  get(id: string): TreeNode {
    const node = this.nodes.get(id);
    if (!node) throw new NotFoundError(`no such node: ${id}`);
    return node;
  }

  parentOf(id: string): string | null {
    return this.get(id).parentId;
  }

  childrenOf(id: string): string[] {
    this.get(id);
    return [...(this.children.get(id) ?? [])];
  }

  createNode(name: string, parentId: string): string {
    this.get(parentId);
    if (!name || name.includes("/")) throw new TreeError(`invalid node name: ${name}`);
    if (this.childByName(parentId, name) !== null)
      throw new TreeError(`duplicate name under ${parentId}: ${name}`);
    const id = `n${++this.counter}`;
    this.nodes.set(id, { id, name, parentId });
    this.children.set(id, new Set());
    this.children.get(parentId)!.add(id);
    return id;
  }

  childByName(parentId: string, name: string): string | null {
    const set = this.children.get(parentId);
    if (!set) return null;
    for (const id of set) {
      if (this.nodes.get(id)!.name === name) return id;
    }
    return null;
  }

  deleteNode(id: string): number {
    const node = this.get(id);
    if (id === this.rootId) throw new TreeError("cannot delete root");
    let removed = 0;
    const stack = [id];
    while (stack.length > 0) {
      const current = stack.pop()!;
      for (const child of this.children.get(current) ?? []) stack.push(child);
      this.children.delete(current);
      this.nodes.delete(current);
      removed++;
    }
    if (node.parentId !== null) this.children.get(node.parentId)?.delete(id);
    return removed;
  }

  isAncestor(ancestorId: string, id: string): boolean {
    let current: string | null = id;
    while (current !== null) {
      if (current === ancestorId) return true;
      current = this.nodes.get(current)?.parentId ?? null;
    }
    return false;
  }

  moveNode(id: string, newParentId: string): void {
    const node = this.get(id);
    this.get(newParentId);
    if (id === this.rootId) throw new MoveValidationError("cannot move root");
    if (id === newParentId || this.isAncestor(id, newParentId))
      throw new MoveValidationError("cannot move node into its own subtree");
    if (node.parentId === newParentId) return;
    if (this.childByName(newParentId, node.name) !== null)
      throw new MoveValidationError(`name conflict at target: ${node.name}`);
    this.children.get(node.parentId!)?.delete(id);
    this.children.get(newParentId)!.add(id);
    node.parentId = newParentId;
  }

  realPath(id: string): string {
    const segments: string[] = [];
    let current: string | null = this.get(id).id;
    while (current !== null && current !== this.rootId) {
      const node: TreeNode = this.nodes.get(current)!;
      segments.unshift(node.name);
      current = node.parentId;
    }
    if (current !== this.rootId) throw new NotFoundError(`node is detached from root: ${id}`);
    return "/" + segments.join("/");
  }

  idAtPath(path: string): string | null {
    let current = this.rootId;
    for (const segment of splitPath(path)) {
      if (segment === "..") {
        current = this.nodes.get(current)!.parentId ?? current;
        continue;
      }
      const next = this.childByName(current, segment);
      if (next === null) return null;
      current = next;
    }
    return current;
  }
}
