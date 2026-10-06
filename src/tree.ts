import { VfsError, ERR } from './errors.ts';
import { isValidName } from './pathutil.ts';

export interface NodeRec {
  id: string;
  name: string;
  parentId: string | null;
  children: string[];
}

/**
 * Plain node store. Node ids are stable: they never change across moves,
 * so aliases and mounts referencing an id keep pointing at the same node.
 */
export class TreeStore {
  private nodes = new Map<string, NodeRec>();
  private seq = 0;
  readonly rootId: string;

  constructor() {
    const root: NodeRec = { id: 'n0', name: '', parentId: null, children: [] };
    this.nodes.set(root.id, root);
    this.rootId = root.id;
  }

  has(id: string): boolean {
    return this.nodes.has(id);
  }

  get(id: string): NodeRec | undefined {
    return this.nodes.get(id);
  }

  createNode(parentId: string, name: string): string {
    const parent = this.mustGet(parentId);
    checkName(name);
    if (this.childByName(parentId, name) !== undefined) {
      throw new VfsError(ERR.AlreadyExists, `node ${name} already exists under ${parentId}`);
    }
    const id = `n${++this.seq}`;
    this.nodes.set(id, { id, name, parentId, children: [] });
    parent.children.push(id);
    return id;
  }

  childByName(parentId: string, name: string): string | undefined {
    const parent = this.nodes.get(parentId);
    if (!parent) return undefined;
    for (const childId of parent.children) {
      if (this.nodes.get(childId)?.name === name) return childId;
    }
    return undefined;
  }

  realPath(id: string): string {
    const names: string[] = [];
    let cur: NodeRec = this.mustGet(id);
    while (cur.parentId !== null) {
      names.unshift(cur.name);
      cur = this.mustGet(cur.parentId);
    }
    return '/' + names.join('/');
  }

  isAncestor(ancestorId: string, id: string): boolean {
    let cur = this.nodes.get(id);
    while (cur && cur.parentId !== null) {
      if (cur.parentId === ancestorId) return true;
      cur = this.nodes.get(cur.parentId);
    }
    return false;
  }

  move(id: string, newParentId: string, newName?: string): void {
    const rec = this.mustGet(id);
    const parent = this.mustGet(newParentId);
    if (id === this.rootId) {
      throw new VfsError(ERR.InvalidPath, 'cannot move the root node');
    }
    const name = newName ?? rec.name;
    checkName(name);
    if (id === newParentId || this.isAncestor(id, newParentId)) {
      throw new VfsError(ERR.WouldCycle, `cannot move ${id} into its own subtree`);
    }
    const existing = this.childByName(newParentId, name);
    if (existing !== undefined && existing !== id) {
      throw new VfsError(ERR.AlreadyExists, `node ${name} already exists under ${newParentId}`);
    }
    if (rec.parentId === newParentId && name === rec.name) return;
    const oldParent = this.mustGet(rec.parentId as string);
    oldParent.children = oldParent.children.filter((child) => child !== id);
    rec.parentId = newParentId;
    rec.name = name;
    parent.children.push(id);
  }

  remove(id: string): void {
    const rec = this.mustGet(id);
    if (id === this.rootId) {
      throw new VfsError(ERR.InvalidPath, 'cannot remove the root node');
    }
    const stack = [id];
    while (stack.length > 0) {
      const cur = this.nodes.get(stack.pop() as string);
      if (!cur) continue;
      for (const child of cur.children) stack.push(child);
      this.nodes.delete(cur.id);
    }
    const parent = this.mustGet(rec.parentId as string);
    parent.children = parent.children.filter((child) => child !== id);
  }

  private mustGet(id: string): NodeRec {
    const rec = this.nodes.get(id);
    if (!rec) throw new VfsError(ERR.NotFound, `node ${id} does not exist`);
    return rec;
  }
}

function checkName(name: string): void {
  if (!isValidName(name)) {
    throw new VfsError(ERR.InvalidPath, `invalid node name: ${name}`);
  }
}
