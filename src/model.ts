export type Mark = string;

export type Affinity = "left" | "right";

export interface Anchor {
  readonly id: string;
  /** Sticks to the text on its left or right side when the node is split. */
  readonly affinity: Affinity;
  /** Character offset within the owning node (0..text.length). */
  readonly offset: number;
}

export interface DocNode {
  readonly id: string;
  readonly text: string;
  /** Unordered set of format marks, e.g. "bold", "italic", "link:https://x". */
  readonly marks: ReadonlySet<Mark>;
  readonly anchors: readonly Anchor[];
}

export interface Doc {
  readonly nodes: readonly DocNode[];
}

let counter = 0;

export function makeNode(
  text: string,
  marks: Iterable<Mark> = [],
  anchors: readonly Anchor[] = [],
  id?: string,
): DocNode {
  return { id: id ?? `n${counter++}`, text, marks: new Set(marks), anchors };
}

export function anchor(id: string, affinity: Affinity = "right", offset = 0): Anchor {
  return { id, affinity, offset };
}

/** Order-insensitive comparison of two mark sets. */
export function marksEqual(a: ReadonlySet<Mark>, b: ReadonlySet<Mark>): boolean {
  if (a.size !== b.size) return false;
  for (const m of a) if (!b.has(m)) return false;
  return true;
}

/** Canonical (sorted) serialization of a mark set. */
export function markKey(marks: ReadonlySet<Mark>): string {
  return [...marks].sort().join("\u001f");
}

export function docLength(doc: Doc): number {
  let n = 0;
  for (const node of doc.nodes) n += node.text.length;
  return n;
}

export function docText(doc: Doc): string {
  return doc.nodes.map((n) => n.text).join("");
}

export function isLinkMark(mark: Mark): boolean {
  return mark === "link" || mark.startsWith("link:");
}

/** Structural validation: links must not nest (a node may carry at most one link mark). */
export function validateDoc(doc: Doc): void {
  for (const node of doc.nodes) {
    let links = 0;
    for (const m of node.marks) if (isLinkMark(m)) links++;
    if (links > 1) {
      throw new Error(`nested links in node "${node.id}": ${[...node.marks].join(", ")}`);
    }
    for (const a of node.anchors) {
      if (a.offset < 0 || a.offset > node.text.length) {
        throw new Error(`anchor "${a.id}" offset ${a.offset} out of range in node "${node.id}"`);
      }
    }
  }
}
