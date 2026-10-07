export interface OutlineNode {
  id: string;
  text: string;
  children: OutlineNode[];
}

export interface DropTarget {
  parentId: string | null;
  index: number;
}

export type MoveFailureReason =
  | "unknown-selection"
  | "unknown-target-parent"
  | "target-inside-selection"
  | "index-out-of-range";

export type MoveResult = { ok: true } | { ok: false; reason: MoveFailureReason };

export interface FlatEntry {
  id: string;
  depth: number;
  parentId: string | null;
}

interface OriginalSpot {
  id: string;
  parentId: string | null;
  index: number;
}

interface MoveRecord {
  movedIds: string[];
  originals: OriginalSpot[];
  targetParentId: string | null;
  targetIndex: number;
}

export function createNode(
  id: string,
  text = id,
  children: OutlineNode[] = [],
): OutlineNode {
  return { id, text, children };
}

export class OutlineDocument {
  private rootChildren: OutlineNode[];
  private nodeMap = new Map<string, OutlineNode>();
  private parentMap = new Map<string, OutlineNode | null>();
  private undoStack: MoveRecord[] = [];
  private redoStack: MoveRecord[] = [];

  constructor(children: OutlineNode[] = []) {
    this.rootChildren = children;
    this.reindex();
  }

  get children(): readonly OutlineNode[] {
    return this.rootChildren;
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  get canRedo(): boolean {
    return this.redoStack.length > 0;
  }

  has(id: string): boolean {
    return this.nodeMap.has(id);
  }

  parentOf(id: string): string | null | undefined {
    if (!this.nodeMap.has(id)) return undefined;
    const parent = this.parentMap.get(id);
    return parent ? parent.id : null;
  }

  isAncestor(ancestorId: string, id: string): boolean {
    let current = this.parentMap.get(id);
    while (current) {
      if (current.id === ancestorId) return true;
      current = this.parentMap.get(current.id);
    }
    return false;
  }

  flatten(): FlatEntry[] {
    const out: FlatEntry[] = [];
    const walk = (
      nodes: OutlineNode[],
      depth: number,
      parentId: string | null,
    ): void => {
      for (const node of nodes) {
        out.push({ id: node.id, depth, parentId });
        walk(node.children, depth + 1, node.id);
      }
    };
    walk(this.rootChildren, 0, null);
    return out;
  }

  move(selectedIds: Iterable<string>, target: DropTarget): MoveResult {
    const ids = [...new Set(selectedIds)];
    for (const id of ids) {
      if (!this.nodeMap.has(id)) {
        return { ok: false, reason: "unknown-selection" };
      }
    }

    const idSet = new Set(ids);
    const effective = ids.filter((id) => {
      let ancestor = this.parentMap.get(id);
      while (ancestor) {
        if (idSet.has(ancestor.id)) return false;
        ancestor = this.parentMap.get(ancestor.id);
      }
      return true;
    });
    if (effective.length === 0) return { ok: true };

    const preOrder = new Map(this.flatten().map((entry, i) => [entry.id, i]));
    effective.sort(
      (a, b) => (preOrder.get(a) as number) - (preOrder.get(b) as number),
    );

    let targetChildren: OutlineNode[];
    if (target.parentId === null) {
      targetChildren = this.rootChildren;
    } else {
      const targetParent = this.nodeMap.get(target.parentId);
      if (!targetParent) {
        return { ok: false, reason: "unknown-target-parent" };
      }
      targetChildren = targetParent.children;
    }

    if (
      target.parentId !== null &&
      (effective.includes(target.parentId) ||
        effective.some((id) => this.isAncestor(id, target.parentId as string)))
    ) {
      return { ok: false, reason: "target-inside-selection" };
    }

    if (
      !Number.isInteger(target.index) ||
      target.index < 0 ||
      target.index > targetChildren.length
    ) {
      return { ok: false, reason: "index-out-of-range" };
    }

    const originals: OriginalSpot[] = effective.map((id) => {
      const parent = this.parentMap.get(id) ?? null;
      const siblings = parent ? parent.children : this.rootChildren;
      return {
        id,
        parentId: parent ? parent.id : null,
        index: siblings.findIndex((node) => node.id === id),
      };
    });

    const removedBefore = originals.filter(
      (spot) => spot.parentId === target.parentId && spot.index < target.index,
    ).length;
    const adjustedIndex = target.index - removedBefore;

    this.detachAll(effective);
    const movedNodes = effective.map(
      (id) => this.nodeMap.get(id) as OutlineNode,
    );
    targetChildren.splice(adjustedIndex, 0, ...movedNodes);

    this.reindex();
    this.undoStack.push({
      movedIds: effective,
      originals,
      targetParentId: target.parentId,
      targetIndex: adjustedIndex,
    });
    this.redoStack = [];
    return { ok: true };
  }

  undo(): boolean {
    const record = this.undoStack.pop();
    if (!record) return false;
    this.detachAll(record.movedIds);
    for (const spot of record.originals) {
      const siblings = this.siblingsOf(spot.parentId);
      siblings.splice(spot.index, 0, this.nodeMap.get(spot.id) as OutlineNode);
    }
    this.reindex();
    this.redoStack.push(record);
    return true;
  }

  redo(): boolean {
    const record = this.redoStack.pop();
    if (!record) return false;
    this.detachAll(record.movedIds);
    const siblings = this.siblingsOf(record.targetParentId);
    const nodes = record.movedIds.map(
      (id) => this.nodeMap.get(id) as OutlineNode,
    );
    siblings.splice(record.targetIndex, 0, ...nodes);
    this.reindex();
    this.undoStack.push(record);
    return true;
  }

  private siblingsOf(parentId: string | null): OutlineNode[] {
    if (parentId === null) return this.rootChildren;
    return (this.nodeMap.get(parentId) as OutlineNode).children;
  }

  private detachAll(ids: string[]): void {
    for (const id of ids) {
      const parent = this.parentMap.get(id) ?? null;
      const siblings = parent ? parent.children : this.rootChildren;
      const at = siblings.findIndex((node) => node.id === id);
      siblings.splice(at, 1);
    }
  }

  private reindex(): void {
    this.nodeMap.clear();
    this.parentMap.clear();
    const walk = (nodes: OutlineNode[], parent: OutlineNode | null): void => {
      for (const node of nodes) {
        this.nodeMap.set(node.id, node);
        this.parentMap.set(node.id, parent);
        walk(node.children, node);
      }
    };
    walk(this.rootChildren, null);
  }
}
