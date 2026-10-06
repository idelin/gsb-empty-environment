import type { Anchor, Doc, DocNode } from "./model.ts";
import { marksEqual } from "./model.ts";

/** Where an original node ended up after normalization. */
export interface NodeMapping {
  /** First node index in the normalized doc. */
  readonly nodeStart: number;
  /** Exclusive end node index in the normalized doc. */
  readonly nodeEnd: number;
  /** Character offset where this node's text starts in the normalized doc. */
  readonly charStart: number;
}

export interface NormalizeResult {
  readonly doc: Doc;
  /**
   * Per original node index: its range in the normalized doc, or null if the
   * node was dropped (empty text and no anchors).
   */
  readonly map: readonly (NodeMapping | null)[];
}

/**
 * Normalize a document:
 *  - drop empty nodes that carry no anchors;
 *  - merge adjacent non-empty nodes whose mark sets are equal (order-insensitive);
 *  - anchored empty nodes are always preserved and never merged away.
 *
 * The result maps every original node to its new range. Normalization is
 * idempotent: normalizing twice yields a structurally identical doc.
 */
export function normalize(doc: Doc): NormalizeResult {
  const nodes: DocNode[] = [];
  const map: (NodeMapping | null)[] = [];
  let charOffset = 0;

  for (const node of doc.nodes) {
    if (node.text.length === 0 && node.anchors.length === 0) {
      map.push(null);
      continue;
    }
    const prev = nodes[nodes.length - 1];
    const canMerge =
      prev !== undefined &&
      prev.text.length > 0 &&
      node.text.length > 0 &&
      marksEqual(prev.marks, node.marks);
    if (canMerge) {
      const shifted: Anchor[] = node.anchors.map((a) => ({
        ...a,
        offset: a.offset + prev.text.length,
      }));
      // Merge in place: the output slot index is unchanged, so previously
      // recorded mappings into this slot stay valid.
      nodes[nodes.length - 1] = {
        ...prev,
        text: prev.text + node.text,
        anchors: [...prev.anchors, ...shifted],
      };
      map.push({ nodeStart: nodes.length - 1, nodeEnd: nodes.length, charStart: charOffset });
    } else {
      nodes.push(node);
      map.push({ nodeStart: nodes.length - 1, nodeEnd: nodes.length, charStart: charOffset });
    }
    charOffset += node.text.length;
  }
  return { doc: { nodes }, map };
}

/** True when normalizing again would change nothing (fixed point). */
export function isNormalized(doc: Doc): boolean {
  const again = normalize(doc);
  if (again.doc.nodes.length !== doc.nodes.length) return false;
  return again.doc.nodes.every((n, i) => n === doc.nodes[i]);
}
