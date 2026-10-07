/**
 * Focus traversal over a virtual node tree.
 *
 * Rules (mirroring sequential focus navigation semantics):
 * - tabIndex > 0: visited first, ordered by tabIndex value, ties broken by document order.
 * - tabIndex === 0 (or `focusable` with no tabIndex): visited next, in document order.
 * - tabIndex < 0: never in the traversal order, but programmatically focusable.
 * - A hidden node makes its whole subtree unfocusable (no exemptions).
 * - A disabled node forms a disabled group: its subtree is unfocusable, except
 *   nodes explicitly marked `exempt`, which lift the barrier for their own subtree.
 */

export interface NodeSpec {
  id: string;
  tabIndex?: number;
  focusable?: boolean;
  hidden?: boolean;
  disabled?: boolean;
  /** Explicitly exempt this node's subtree from an ancestor disabled group. */
  exempt?: boolean;
  children?: NodeSpec[];
}

export interface AttributePatch {
  tabIndex?: number | null;
  focusable?: boolean;
  hidden?: boolean;
  disabled?: boolean;
  exempt?: boolean;
}

export type FocusKind = "tab-order" | "programmatic" | "unfocusable";

interface TreeNode {
  id: string;
  tabIndex?: number;
  focusable: boolean;
  hidden: boolean;
  disabled: boolean;
  exempt: boolean;
  parent: TreeNode | null;
  children: TreeNode[];
}

interface ExpandedEntry {
  id: string;
  tabIndex: number;
  documentIndex: number;
}

export class DuplicateIdError extends Error {
  constructor(id: string) {
    super(`Duplicate node id: "${id}"`);
    this.name = "DuplicateIdError";
  }
}

export class UnknownIdError extends Error {
  constructor(id: string) {
    super(`Unknown node id: "${id}"`);
    this.name = "UnknownIdError";
  }
}

export class FocusTree {
  private roots: TreeNode[] = [];
  private byId = new Map<string, TreeNode>();

  constructor(specs: NodeSpec[] = []) {
    for (const spec of specs) this.addNode(null, spec);
  }

  /** Attach a new subtree under `parentId` (or as a root when null). */
  addNode(parentId: string | null, spec: NodeSpec): void {
    const parent = parentId === null ? null : this.require(parentId);
    const node = this.materialize(spec, parent);
    if (parent) parent.children.push(node);
    else this.roots.push(node);
  }

  /** Remove a node and its subtree. */
  removeNode(id: string): void {
    const node = this.require(id);
    const siblings = node.parent ? node.parent.children : this.roots;
    siblings.splice(siblings.indexOf(node), 1);
    for (const n of this.flatten(node)) this.byId.delete(n.id);
  }

  /**
   * Apply dynamic attribute updates. Passing `tabIndex: null` clears the
   * attribute. Every update triggers a full-tree re-expansion on next read.
   */
  updateAttributes(id: string, patch: AttributePatch): void {
    const node = this.require(id);
    if (patch.tabIndex !== undefined) {
      node.tabIndex = patch.tabIndex === null ? undefined : patch.tabIndex;
    }
    if (patch.focusable !== undefined) node.focusable = patch.focusable;
    if (patch.hidden !== undefined) node.hidden = patch.hidden;
    if (patch.disabled !== undefined) node.disabled = patch.disabled;
    if (patch.exempt !== undefined) node.exempt = patch.exempt;
  }

  /** Rename a node; rejects ids already present in the tree. */
  renameId(id: string, nextId: string): void {
    const node = this.require(id);
    if (nextId !== id && this.byId.has(nextId)) throw new DuplicateIdError(nextId);
    this.byId.delete(id);
    node.id = nextId;
    this.byId.set(nextId, node);
  }

  has(id: string): boolean {
    return this.byId.has(id);
  }

  /** Ids in sequential focus (tab) traversal order. */
  traversalOrder(): string[] {
    const { positive, zero } = this.expand();
    positive.sort((a, b) => a.tabIndex - b.tabIndex || a.documentIndex - b.documentIndex);
    return [...positive, ...zero].map((e) => e.id);
  }

  /** Ids focusable only programmatically (tabIndex < 0), in document order. */
  programmaticOnly(): string[] {
    return this.expand().programmatic.map((e) => e.id);
  }

  classify(id: string): FocusKind {
    const { positive, zero, programmatic } = this.expand();
    if (positive.some((e) => e.id === id) || zero.some((e) => e.id === id)) return "tab-order";
    if (programmatic.some((e) => e.id === id)) return "programmatic";
    if (!this.byId.has(id)) throw new UnknownIdError(id);
    return "unfocusable";
  }

  isFocusable(id: string): boolean {
    return this.classify(id) !== "unfocusable";
  }

  /**
   * Full-tree expansion: depth-first walk from the roots, propagating hidden
   * and disabled context, classifying every focusable node. Document order is
   * assigned by visit sequence.
   */
  private expand(): {
    positive: ExpandedEntry[];
    zero: ExpandedEntry[];
    programmatic: ExpandedEntry[];
  } {
    const positive: ExpandedEntry[] = [];
    const zero: ExpandedEntry[] = [];
    const programmatic: ExpandedEntry[] = [];
    let documentIndex = 0;

    const visit = (node: TreeNode, hiddenCtx: boolean, disabledCtx: boolean): void => {
      const index = documentIndex++;
      const hidden = hiddenCtx || node.hidden;
      const disabled = node.exempt ? false : disabledCtx || node.disabled;
      if (!hidden && !disabled) {
        const entry = { id: node.id, tabIndex: node.tabIndex ?? 0, documentIndex: index };
        if (node.tabIndex !== undefined && node.tabIndex > 0) positive.push(entry);
        else if (node.tabIndex !== undefined && node.tabIndex < 0) programmatic.push(entry);
        else if (node.focusable || node.tabIndex !== undefined) zero.push(entry);
      }
      for (const child of node.children) visit(child, hidden, disabled);
    };

    for (const root of this.roots) visit(root, false, false);
    return { positive, zero, programmatic };
  }

  private materialize(spec: NodeSpec, parent: TreeNode | null): TreeNode {
    if (this.byId.has(spec.id)) throw new DuplicateIdError(spec.id);
    const node: TreeNode = {
      id: spec.id,
      tabIndex: spec.tabIndex,
      focusable: spec.focusable ?? false,
      hidden: spec.hidden ?? false,
      disabled: spec.disabled ?? false,
      exempt: spec.exempt ?? false,
      parent,
      children: [],
    };
    this.byId.set(node.id, node);
    for (const child of spec.children ?? []) {
      node.children.push(this.materialize(child, node));
    }
    return node;
  }

  private flatten(node: TreeNode): TreeNode[] {
    return [node, ...node.children.flatMap((c) => this.flatten(c))];
  }

  private require(id: string): TreeNode {
    const node = this.byId.get(id);
    if (!node) throw new UnknownIdError(id);
    return node;
  }
}
