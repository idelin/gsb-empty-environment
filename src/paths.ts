/**
 * Instance paths are JSON-Pointer-like strings: "" is the root,
 * segments are joined with "/" and escaped per RFC 6901 (~0, ~1).
 */
export type Path = string;

export const ROOT: Path = '';

export function escapeSegment(seg: string): string {
  return seg.replace(/~/g, '~0').replace(/\//g, '~1');
}

export function unescapeSegment(seg: string): string {
  return seg.replace(/~1/g, '/').replace(/~0/g, '~');
}

export function joinPath(base: Path, seg: string | number): Path {
  return `${base}/${escapeSegment(String(seg))}`;
}

export function parsePath(p: Path): string[] {
  if (p === '') return [];
  return p.slice(1).split('/').map(unescapeSegment);
}

export function pathDepth(p: Path): number {
  return p === '' ? 0 : p.split('/').length - 1;
}

export function parentPath(p: Path): Path | null {
  if (p === '') return null;
  const i = p.lastIndexOf('/');
  return i === 0 ? '' : p.slice(0, i);
}

/** True when `a` equals `b` or is an ancestor segment-prefix of `b`. */
export function isPrefixOf(a: Path, b: Path): boolean {
  if (a === '') return true;
  return b === a || b.startsWith(a + '/');
}

/**
 * Two paths are "related" when one contains the other. A write at path P
 * can only affect rules whose dependencies are related to P.
 */
export function pathsRelated(a: Path, b: Path): boolean {
  return isPrefixOf(a, b) || isPrefixOf(b, a);
}

export function getAt(doc: unknown, p: Path): unknown {
  let cur: unknown = doc;
  for (const seg of parsePath(p)) {
    if (cur === null || typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

function navigateToParent(doc: unknown, p: Path): { parent: Record<string, unknown> | unknown[]; key: string } {
  const segs = parsePath(p);
  if (segs.length === 0) throw new Error('cannot modify the document root; use replaceDocument()');
  let cur: unknown = doc;
  for (let i = 0; i < segs.length - 1; i++) {
    if (cur === null || typeof cur !== 'object') {
      throw new Error(`cannot traverse into non-container at segment "${segs[i]}" of "${p}"`);
    }
    cur = (cur as Record<string, unknown>)[segs[i]];
  }
  if (cur === null || typeof cur !== 'object') {
    throw new Error(`parent of "${p}" is not a container`);
  }
  return { parent: cur as Record<string, unknown> | unknown[], key: segs[segs.length - 1] };
}

export function setAt(doc: unknown, p: Path, value: unknown): void {
  const { parent, key } = navigateToParent(doc, p);
  if (Array.isArray(parent)) {
    const idx = Number(key);
    if (!Number.isInteger(idx) || idx < 0 || idx > parent.length) {
      throw new Error(`array index "${key}" out of bounds for "${p}"`);
    }
    parent[idx] = value;
  } else {
    parent[key] = value;
  }
}

export function deleteAt(doc: unknown, p: Path): void {
  const { parent, key } = navigateToParent(doc, p);
  if (Array.isArray(parent)) {
    const idx = Number(key);
    if (!Number.isInteger(idx) || idx < 0 || idx >= parent.length) {
      throw new Error(`array index "${key}" out of bounds for "${p}"`);
    }
    parent.splice(idx, 1);
  } else {
    delete (parent as Record<string, unknown>)[key];
  }
}
