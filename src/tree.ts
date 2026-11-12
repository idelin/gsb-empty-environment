import type { AccessNode } from './types.ts';

export interface TreeIndex {
  nodes: Map<string, AccessNode>;
  parent: Map<string, string | null>;
  children: Map<string, string[]>;
  /**
   * Nearest form/dialog ancestor id for each node (inclusive).
   * Nodes outside any form share the `null` bucket and may only bind
   * other form-less nodes.
   */
  formRoot: Map<string, string | null>;
  roots: string[];
}

export function indexTree(nodes: AccessNode[], formRoles: string[]): TreeIndex {
  const byId = new Map<string, AccessNode>();
  const parent = new Map<string, string | null>();
  const children = new Map<string, string[]>();

  for (const node of nodes) {
    if (byId.has(node.id)) {
      throw new Error(`Duplicate node id in accessibility tree: ${node.id}`);
    }
    byId.set(node.id, node);
    parent.set(node.id, node.parentId ?? null);
    children.set(node.id, []);
  }

  const roots: string[] = [];
  for (const node of nodes) {
    const pid = node.parentId ?? null;
    if (pid === null) {
      roots.push(node.id);
      continue;
    }
    if (!byId.has(pid)) {
      throw new Error(`Node ${node.id} references unknown parent ${pid}`);
    }
    children.get(pid)!.push(node.id);
  }

  const formRoleSet = new Set(formRoles);
  const formRoot = new Map<string, string | null>();

  const walk = (id: string, nearestForm: string | null): void => {
    const node = byId.get(id)!;
    const currentForm = formRoleSet.has(node.role) ? id : nearestForm;
    formRoot.set(id, currentForm);
    for (const childId of children.get(id) ?? []) {
      walk(childId, currentForm);
    }
  };
  for (const rootId of roots) {
    walk(rootId, null);
  }

  return { nodes: byId, parent, children, formRoot, roots };
}

/** Two nodes share a form ancestry only when their form bucket matches. */
export function sharesFormAncestor(index: TreeIndex, labelId: string, controlId: string): boolean {
  return index.formRoot.get(labelId) === index.formRoot.get(controlId);
}
