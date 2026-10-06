import type { Anchor, Doc, DocNode, Mark } from "./model.ts";

/** One rendered character plus its sorted mark list. */
export interface FlatCell {
  readonly char: string;
  readonly marks: readonly Mark[];
}

/** Anchor pinned to a character boundary (offset is always 0 at a boundary). */
export interface BoundaryAnchor {
  readonly id: string;
  readonly affinity: Anchor["affinity"];
}

/**
 * Flat ground-truth model: one cell per rendered character, plus
 * cells.length + 1 boundary slots carrying the anchors at that position.
 * `owners[i]` is the source node index of cell i (used to keep node identity).
 */
export interface FlatDoc {
  readonly cells: readonly FlatCell[];
  readonly boundaries: readonly (readonly BoundaryAnchor[])[];
  readonly owners: readonly number[];
}

export function flatten(doc: Doc): FlatDoc {
  const cells: FlatCell[] = [];
  const boundaries: BoundaryAnchor[][] = [[]];
  const owners: number[] = [];
  doc.nodes.forEach((node, nodeIndex) => {
    // Anchors sit at the boundary after `offset` characters of this node.
    const startBoundary = boundaries.length - 1;
    for (const ch of node.text) {
      cells.push({ char: ch, marks: [...node.marks].sort() });
      owners.push(nodeIndex);
      boundaries.push([]);
    }
    for (const a of node.anchors) {
      boundaries[startBoundary + a.offset].push({ id: a.id, affinity: a.affinity });
    }
  });
  // Canonical order at each boundary: anchor ordering at the same position is
  // not meaningful, so sort to make equivalence checks deterministic.
  for (const b of boundaries) {
    b.sort((x, y) => (x.id === y.id ? x.affinity.localeCompare(y.affinity) : x.id < y.id ? -1 : 1));
  }
  return { cells, boundaries, owners };
}

/** Rendered characters of a doc, e.g. "hello". */
export function renderChars(doc: Doc): string {
  let out = "";
  for (const node of doc.nodes) out += node.text;
  return out;
}

/** Per-character sorted mark sequence, e.g. [["bold"], [], ["bold","italic"]]. */
export function markSequence(doc: Doc): readonly (readonly Mark[])[] {
  const seq: Mark[][] = [];
  for (const node of doc.nodes) {
    const marks = [...node.marks].sort();
    for (let i = 0; i < node.text.length; i++) seq.push(marks);
  }
  return seq;
}

function anchorsEqual(a: readonly BoundaryAnchor[], b: readonly BoundaryAnchor[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    if (a[i].id !== b[i].id || a[i].affinity !== b[i].affinity) return false;
  }
  return true;
}

/**
 * Equivalence by rendered characters + per-character mark sequence + anchors
 * at character boundaries. Node identity and mark insertion order are ignored.
 */
export function equivalent(a: Doc, b: Doc): boolean {
  const fa = flatten(a);
  const fb = flatten(b);
  if (fa.cells.length !== fb.cells.length) return false;
  for (let i = 0; i < fa.cells.length; i++) {
    const ca = fa.cells[i];
    const cb = fb.cells[i];
    if (ca.char !== cb.char) return false;
    if (ca.marks.length !== cb.marks.length) return false;
    for (let j = 0; j < ca.marks.length; j++) if (ca.marks[j] !== cb.marks[j]) return false;
  }
  for (let i = 0; i < fa.boundaries.length; i++) {
    if (!anchorsEqual(fa.boundaries[i], fb.boundaries[i])) return false;
  }
  return true;
}

export type IdGen = (owner: number | null, seq: number) => string;

const defaultIdGen: IdGen = (() => {
  let n = 0;
  return () => `f${n++}`;
})();

/**
 * Rebuild nodes from a flat model. Consecutive cells with equal mark sets are
 * grouped into one node. Anchors attach to a neighbouring node by affinity:
 * left-affinity anchors join the node ending at their boundary, right-affinity
 * anchors join the node starting there. If the doc has no characters at all,
 * anchors are kept on a single empty node (anchored empty nodes survive).
 */
export function fromFlat(flat: FlatDoc, idGen: IdGen = defaultIdGen): Doc {
  const { cells, boundaries, owners } = flat;
  if (cells.length === 0) {
    const anchors: Anchor[] = (boundaries[0] ?? []).map((b) => ({
      id: b.id,
      affinity: b.affinity,
      offset: 0,
    }));
    return { nodes: anchors.length ? [{ id: idGen(null, 0), text: "", marks: new Set(), anchors }] : [] };
  }

  // Cut wherever the mark set or the source owner changes.
  const cuts: number[] = [0];
  for (let i = 1; i < cells.length; i++) {
    if (
      owners[i] !== owners[i - 1] ||
      markKeyOf(cells[i].marks) !== markKeyOf(cells[i - 1].marks)
    ) {
      cuts.push(i);
    }
  }
  cuts.push(cells.length);

  const nodes: DocNode[] = [];
  const ownerSeq = new Map<number, number>();
  // segment index -> anchors, gathered per affinity rules below
  const segmentAnchors: Anchor[][] = cuts.slice(0, -1).map(() => []);

  const segmentOf = (charIndex: number): number => {
    // binary search: last cut <= charIndex
    let lo = 0;
    let hi = cuts.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (cuts[mid] <= charIndex) lo = mid;
      else hi = mid - 1;
    }
    return lo;
  };

  for (let k = 0; k < boundaries.length; k++) {
    for (const b of boundaries[k]) {
      let seg: number;
      if (k === 0) {
        seg = 0;
      } else if (k === cells.length) {
        seg = cuts.length - 2;
      } else if (b.affinity === "left") {
        seg = segmentOf(k - 1);
      } else {
        seg = segmentOf(k);
      }
      segmentAnchors[seg].push({ id: b.id, affinity: b.affinity, offset: k - cuts[seg] });
    }
  }

  for (let s = 0; s < cuts.length - 1; s++) {
    const start = cuts[s];
    const end = cuts[s + 1];
    const owner = owners[start];
    const seq = ownerSeq.get(owner) ?? 0;
    ownerSeq.set(owner, seq + 1);
    nodes.push({
      id: idGen(owner, seq),
      text: cells.slice(start, end).map((c) => c.char).join(""),
      marks: new Set(cells[start].marks),
      anchors: segmentAnchors[s],
    });
  }
  return { nodes };
}

function markKeyOf(marks: readonly Mark[]): string {
  return marks.join("\u001f");
}
