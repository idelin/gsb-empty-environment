/** Direction level: even = left-to-right, odd = right-to-left. */
export type Level = number;

/**
 * Which side of a boundary a caret is attached to.
 * - "backward": caret belongs to the cluster immediately *before* the logical offset
 * - "forward":  caret belongs to the cluster immediately *after*  the logical offset
 */
export type Affinity = "backward" | "forward";

export type ResolvedLevel = Level | undefined;

export interface TextNode {
  /** Optional stable id; assigned when absent. */
  id?: string;
  text: string;
  /** Pre-resolved levels per grapheme cluster; length must match cluster count. */
  levels?: Level[];
  /**
   * Explicit clusters (e.g. produced by a fixture's own UAX#29 splitter).
   * When omitted, clusters are derived with the project's minimal splitter.
   */
  clusters?: string[];
  /** Container default; used when a cluster level entry is undefined. */
  level?: Level;
}

export interface ContainerNode {
  id?: string;
  children: DocNode[];
  level?: Level;
}

export type DocNode = ContainerNode | TextNode;

export function isTextNode(node: DocNode): node is TextNode {
  return typeof (node as TextNode).text === "string";
}

export interface FlatCluster {
  /** Global logical index. */
  index: number;
  text: string;
  level: Level;
  nodeId: string;
}

/** Glyph metrics supplied by a fixture — no browser layout engine involved. */
export interface GlyphMetrics {
  /** Advance width of a cluster within a given level context. */
  clusterWidth(cluster: FlatCluster): number;
  /** Line box height. */
  lineHeight: number;
}

export interface LayoutOptions {
  maxWidth: number;
  metrics: GlyphMetrics;
}
