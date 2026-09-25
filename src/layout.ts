/**
 * Pure layout/measurement helpers. All geometry comes from fixtures
 * (a serialized node tree + glyph metrics); no browser layout engine
 * is involved in the core computation.
 */

export interface GlyphMetrics {
  /** Advance width used for glyphs not listed explicitly. */
  defaultAdvance: number;
  /** Per-glyph advance widths, keyed by single character. */
  advances: Record<string, number>;
}

export interface LayoutNode {
  id: string;
  role?: string;
  /** Content-box width in px, when the fixture defines one. */
  width?: number;
  children?: LayoutNode[];
}

export function measureText(text: string, metrics: GlyphMetrics): number {
  let width = 0;
  for (const ch of text) {
    width += metrics.advances[ch] ?? metrics.defaultAdvance;
  }
  return width;
}

export function findByRole(node: LayoutNode, role: string): LayoutNode | undefined {
  if (node.role === role) return node;
  for (const child of node.children ?? []) {
    const hit = findByRole(child, role);
    if (hit) return hit;
  }
  return undefined;
}

/** Greedy word wrap by measured width; over-long words are hard-split. */
export function wrapText(text: string, maxWidth: number, metrics: GlyphMetrics): string[] {
  const lines: string[] = [];
  let line = '';
  for (const word of text.split(' ')) {
    const candidate = line === '' ? word : `${line} ${word}`;
    if (measureText(candidate, metrics) <= maxWidth) {
      line = candidate;
      continue;
    }
    if (line !== '') {
      lines.push(line);
      line = '';
    }
    // Hard-split a word that alone exceeds maxWidth.
    let rest = word;
    while (measureText(rest, metrics) > maxWidth) {
      let cut = rest.length;
      while (cut > 1 && measureText(rest.slice(0, cut), metrics) > maxWidth) cut--;
      lines.push(rest.slice(0, cut));
      rest = rest.slice(cut);
    }
    line = rest;
  }
  if (line !== '') lines.push(line);
  return lines;
}

export interface AnnouncementLayout {
  region: LayoutNode;
  lines: string[];
  textWidth: number;
}

/** Lay out an announcement inside the fixture node with role "status". */
export function layoutAnnouncement(
  root: LayoutNode,
  text: string,
  metrics: GlyphMetrics,
): AnnouncementLayout {
  const region = findByRole(root, 'status');
  if (!region) throw new Error('fixture node tree has no node with role "status"');
  if (typeof region.width !== 'number') {
    throw new Error(`status region "${region.id}" has no fixture width`);
  }
  const lines = wrapText(text, region.width, metrics);
  return { region, lines, textWidth: measureText(text, metrics) };
}
