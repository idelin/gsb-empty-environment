import {
  InvalidSpecError,
  computeProgrammaticOrder,
  computeTabOrder,
  expandTree,
} from './traversal.ts';
import type { FocusInfo, UpdateAttrs, VNodeSpec } from './types.ts';

export class DuplicateIdError extends Error {
  constructor(id: string) {
    super(`duplicate id "${id}"`);
    this.name = 'DuplicateIdError';
  }
}

export class UnknownNodeError extends Error {
  constructor(id: string) {
    super(`unknown node id "${id}"`);
    this.name = 'UnknownNodeError';
  }
}

/**
 * A virtual node tree with dynamic attribute updates. All queries are
 * answered by full-tree expansion, so results after any sequence of
 * mutations always match a tree freshly built from the current spec.
 */
export class FocusTree {
  #root: VNodeSpec;
  #ids = new Set<string>();

  constructor(root: VNodeSpec) {
    this.#root = root;
    this.#reindex();
  }

  static fromSpec(spec: VNodeSpec): FocusTree {
    return new FocusTree(spec);
  }

  /** Full-tree expansion: effective focus state of every node. */
  expand(): FocusInfo[] {
    return expandTree(this.#root);
  }

  /** Sequential (Tab) focus order as a list of ids. */
  tabOrder(): string[] {
    return computeTabOrder(this.expand());
  }

  /** Programmatically-focusable-only ids (negative tabindex), document order. */
  programmaticOrder(): string[] {
    return computeProgrammaticOrder(this.expand());
  }

  info(id: string): FocusInfo {
    const found = this.expand().find((entry) => entry.id === id);
    if (!found) throw new UnknownNodeError(id);
    return found;
  }

  isFocusable(id: string): boolean {
    return this.info(id).focusable;
  }

  isSequentiallyFocusable(id: string): boolean {
    return this.info(id).sequentiallyFocusable;
  }

  /** Deep-cloned current spec; rebuilding from it must yield identical results. */
  snapshot(): VNodeSpec {
    return structuredClone(this.#root);
  }

  /**
   * Applies attribute changes to one node. `tabindex: null` removes the
   * attribute. Renaming via `id` is allowed but rejects duplicates.
   * Structural changes go through {@link insert} / {@link remove}.
   */
  update(id: string, attrs: UpdateAttrs): void {
    const node = this.#find(id);
    if (!node) throw new UnknownNodeError(id);
    if ('children' in attrs) {
      throw new InvalidSpecError('update() cannot change children; use insert()/remove()');
    }
    if (attrs.tabindex !== undefined && attrs.tabindex !== null && !Number.isInteger(attrs.tabindex)) {
      throw new InvalidSpecError(`node "${id}": tabindex must be an integer`);
    }
    if (attrs.id !== undefined && attrs.id !== id && this.#ids.has(attrs.id)) {
      throw new DuplicateIdError(attrs.id);
    }

    if (attrs.tabindex !== undefined) {
      if (attrs.tabindex === null) delete node.tabindex;
      else node.tabindex = attrs.tabindex;
    }
    if (attrs.hidden !== undefined) node.hidden = attrs.hidden;
    if (attrs.disabled !== undefined) node.disabled = attrs.disabled;
    if (attrs.focusable !== undefined) node.focusable = attrs.focusable;
    if (attrs.group !== undefined) node.group = attrs.group;
    if (attrs.exempt !== undefined) node.exempt = [...attrs.exempt];
    if (attrs.id !== undefined && attrs.id !== id) {
      node.id = attrs.id;
      this.#reindex();
    }
  }

  /** Attaches a subtree under `parentId`; rejects duplicate ids anywhere. */
  insert(parentId: string, spec: VNodeSpec, index?: number): void {
    const parent = this.#find(parentId);
    if (!parent) throw new UnknownNodeError(parentId);
    const incoming = new Set<string>();
    const collect = (node: VNodeSpec): void => {
      if (incoming.has(node.id) || this.#ids.has(node.id)) {
        throw new DuplicateIdError(node.id);
      }
      incoming.add(node.id);
      for (const child of node.children ?? []) collect(child);
    };
    collect(spec);

    parent.children ??= [];
    const at = index === undefined ? parent.children.length : index;
    if (at < 0 || at > parent.children.length) {
      throw new InvalidSpecError(`insert index ${at} out of range`);
    }
    parent.children.splice(at, 0, spec);
    this.#reindex();
  }

  /** Detaches a node (and its subtree). The root cannot be removed. */
  remove(id: string): void {
    const removed = this.#removeFrom(this.#root, id);
    if (!removed) {
      if (this.#root.id === id) {
        throw new InvalidSpecError('cannot remove the root node');
      }
      throw new UnknownNodeError(id);
    }
    this.#reindex();
  }

  #removeFrom(node: VNodeSpec, id: string): boolean {
    const children = node.children ?? [];
    const at = children.findIndex((child) => child.id === id);
    if (at >= 0) {
      children.splice(at, 1);
      return true;
    }
    return children.some((child) => this.#removeFrom(child, id));
  }

  #find(id: string): VNodeSpec | undefined {
    const stack: VNodeSpec[] = [this.#root];
    while (stack.length > 0) {
      const node = stack.pop() as VNodeSpec;
      if (node.id === id) return node;
      for (const child of node.children ?? []) stack.push(child);
    }
    return undefined;
  }

  #reindex(): void {
    this.#ids.clear();
    const walk = (node: VNodeSpec): void => {
      if (this.#ids.has(node.id)) throw new DuplicateIdError(node.id);
      this.#ids.add(node.id);
      for (const child of node.children ?? []) walk(child);
    };
    walk(this.#root);
  }
}
