import { buildIndex, computeDescription, computeName } from './compute.ts';
import type { DepKey, GlyphMetrics, NameResult, TreeNode } from './types.ts';

export interface ChangeNotification {
  nodeId: string;
  version: number;
  oldName: string;
  newName: string;
  oldDescription: string;
  newDescription: string;
}

interface CacheEntry {
  version: number;
  result: NameResult;
  deps: Set<DepKey>;
}

export class AccNameEngine {
  private readonly root: TreeNode;
  private readonly metrics: GlyphMetrics;
  private index: Map<string, TreeNode>;
  private parentOf = new Map<string, TreeNode>();
  private version = 0;
  private readonly cache = new Map<string, CacheEntry>();
  private readonly reverse = new Map<DepKey, Set<string>>();
  private readonly history: Array<Set<DepKey>> = [new Set()];
  private readonly listeners: Array<(notification: ChangeNotification) => void> = [];

  constructor(root: TreeNode, metrics: GlyphMetrics) {
    this.root = root;
    this.metrics = metrics;
    this.index = buildIndex(root);
    this.reindexParents();
  }

  get treeVersion(): number {
    return this.version;
  }

  onChange(listener: (notification: ChangeNotification) => void): void {
    this.listeners.push(listener);
  }

  getName(nodeId: string): string {
    return this.ensure(nodeId).name;
  }

  getDescription(nodeId: string): string {
    return this.ensure(nodeId).description;
  }

  getDependencies(nodeId: string): DepKey[] {
    this.ensure(nodeId);
    return [...(this.cache.get(nodeId)?.deps ?? [])].sort();
  }

  setText(nodeId: string, text: string): void {
    this.node(nodeId).text = text;
    this.mutate([`${nodeId}:text`]);
  }

  setAttribute(nodeId: string, name: string, value: string): void {
    const target = this.node(nodeId);
    const previous = target.attrs[name];
    target.attrs[name] = value;
    const keys: DepKey[] = [`${nodeId}:attr:${name}`];
    if (name === 'for') {
      if (previous !== undefined) keys.push(`@labelfor:${previous}`);
      keys.push(`@labelfor:${value}`);
    }
    this.mutate(keys);
  }

  removeAttribute(nodeId: string, name: string): void {
    const target = this.node(nodeId);
    const previous = target.attrs[name];
    delete target.attrs[name];
    const keys: DepKey[] = [`${nodeId}:attr:${name}`];
    if (name === 'for' && previous !== undefined) keys.push(`@labelfor:${previous}`);
    this.mutate(keys);
  }

  setRole(nodeId: string, role: string): void {
    this.node(nodeId).role = role;
    this.mutate([`${nodeId}:role`]);
  }

  setHidden(nodeId: string, hidden: boolean): void {
    this.node(nodeId).hidden = hidden;
    this.mutate([`${nodeId}:hidden`]);
  }

  appendChild(parentId: string, child: TreeNode): void {
    this.node(parentId).children.push(child);
    this.reindex();
    const keys: DepKey[] = [`children:${parentId}`, `@id:${child.id}`];
    if (child.role === 'label' && child.attrs['for']) {
      keys.push(`@labelfor:${child.attrs['for']}`);
    }
    this.mutate(keys);
  }

  removeNode(nodeId: string): void {
    const parent = this.parentOf.get(nodeId);
    if (!parent) throw new Error(`cannot remove root or unknown node: ${nodeId}`);
    const position = parent.children.findIndex((child) => child.id === nodeId);
    const [removed] = parent.children.splice(position, 1);
    const keys: DepKey[] = [`children:${parent.id}`];
    const collect = (node: TreeNode): void => {
      keys.push(
        `@id:${node.id}`,
        `${node.id}:text`,
        `${node.id}:hidden`,
        `${node.id}:role`,
        `children:${node.id}`,
      );
      for (const name of Object.keys(node.attrs)) keys.push(`${node.id}:attr:${name}`);
      if (node.role === 'label' && node.attrs['for']) {
        keys.push(`@labelfor:${node.attrs['for']}`);
      }
      node.children.forEach(collect);
    };
    collect(removed);
    this.reindex();
    this.mutate(keys);
  }

  private node(nodeId: string): TreeNode {
    const found = this.index.get(nodeId);
    if (!found) throw new Error(`unknown node: ${nodeId}`);
    return found;
  }

  private reindex(): void {
    this.index = buildIndex(this.root);
    this.reindexParents();
  }

  private reindexParents(): void {
    this.parentOf = new Map();
    const walk = (node: TreeNode): void => {
      for (const child of node.children) {
        if (!this.parentOf.has(child.id)) this.parentOf.set(child.id, node);
        walk(child);
      }
    };
    walk(this.root);
  }

  private ensure(nodeId: string): NameResult {
    const existing = this.cache.get(nodeId);
    if (existing) {
      if (existing.version === this.version) return existing.result;
      if (!this.isDirty(existing)) {
        existing.version = this.version;
        return existing.result;
      }
    }
    const deps = new Set<DepKey>();
    const ctx = { index: this.index, metrics: this.metrics, deps };
    const result: NameResult = {
      name: computeName(nodeId, ctx),
      description: computeDescription(nodeId, ctx),
    };
    if (existing) {
      for (const key of existing.deps) this.reverse.get(key)?.delete(nodeId);
    }
    for (const key of deps) {
      let consumers = this.reverse.get(key);
      if (!consumers) {
        consumers = new Set();
        this.reverse.set(key, consumers);
      }
      consumers.add(nodeId);
    }
    this.cache.set(nodeId, { version: this.version, result, deps });
    if (
      existing &&
      (existing.result.name !== result.name ||
        existing.result.description !== result.description)
    ) {
      const notification: ChangeNotification = {
        nodeId,
        version: this.version,
        oldName: existing.result.name,
        newName: result.name,
        oldDescription: existing.result.description,
        newDescription: result.description,
      };
      for (const listener of this.listeners) listener(notification);
    }
    return result;
  }

  private isDirty(entry: CacheEntry): boolean {
    for (let v = entry.version + 1; v <= this.version; v += 1) {
      for (const key of this.history[v]) {
        if (entry.deps.has(key)) return true;
      }
    }
    return false;
  }

  private mutate(changedKeys: DepKey[]): void {
    this.version += 1;
    const changed = new Set(changedKeys);
    this.history[this.version] = changed;
    const affected = new Set<string>();
    for (const key of changed) {
      for (const consumer of this.reverse.get(key) ?? []) affected.add(consumer);
    }
    for (const nodeId of affected) this.ensure(nodeId);
  }
}
