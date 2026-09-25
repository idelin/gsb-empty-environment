export type NodeId = string;

export interface NodeData {
  id: NodeId;
  label: string;
  disabled?: boolean;
  /** Eager children, present at construction/insert time. */
  children?: NodeData[];
  /** Marks a lazy branch: children must be loaded asynchronously. */
  hasChildren?: boolean;
}

/** Glyph metrics come from fixtures, never from a browser layout engine. */
export interface GlyphMetrics {
  defaultAdvance: number;
  advance: Record<string, number>;
}

export type TreeEvent =
  | { type: 'expand'; id: NodeId }
  | { type: 'collapse'; id: NodeId }
  | { type: 'insert'; parentId: NodeId | null; ids: NodeId[]; index: number }
  | { type: 'remove'; ids: NodeId[] } // preorder subtree ids, ids[0] is the detached top node
  | { type: 'move'; id: NodeId; toParent: NodeId | null; index: number }
  | { type: 'meta'; id: NodeId } // label/disabled/loaded changed
  | { type: 'filter' };
