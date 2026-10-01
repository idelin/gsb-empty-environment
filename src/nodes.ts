import { measureText } from './metrics.ts';
import type { Node, PenaltyNode } from './types.ts';

/**
 * Hyphenation fixture: word -> syllables. Only words present here can be
 * broken inside; every other word is a single unbreakable box.
 */
export type HyphenationSpec = Readonly<Record<string, readonly string[]>>;

export interface GlueSpec {
  width: number;
  stretch: number;
  shrink: number;
}

export const DEFAULT_GLUE: GlueSpec = { width: 3, stretch: 2, shrink: 1 };
export const HYPHEN_PENALTY = 50;

/** Build the node tree for a paragraph from fixture metrics + hyphenation. */
export function paragraphToNodes(
  text: string,
  hyphenation: HyphenationSpec = {},
  glue: GlueSpec = DEFAULT_GLUE,
): Node[] {
  const nodes: Node[] = [];
  const words = text.split(/\s+/).filter((w) => w.length > 0);
  words.forEach((word, wi) => {
    if (wi > 0) nodes.push({ kind: 'glue', ...glue });
    const syllables = hyphenation[word];
    if (syllables && syllables.length > 1) {
      syllables.forEach((syl, si) => {
        if (si > 0) {
          const pen: PenaltyNode = {
            kind: 'penalty',
            penalty: HYPHEN_PENALTY,
            width: measureText('-'),
            flagged: true,
            text: '-',
          };
          nodes.push(pen);
        }
        nodes.push({ kind: 'box', width: measureText(syl), text: syl });
      });
    } else {
      nodes.push({ kind: 'box', width: measureText(word), text: word });
    }
  });
  return nodes;
}

/**
 * Break positions are indices between nodes: 0 .. nodes.length.
 * A break may occur after a glue node, at a penalty node, or at the end.
 * Returns sorted unique break positions.
 */
export function breakPositions(nodes: Node[]): number[] {
  const positions: number[] = [0];
  for (let i = 0; i < nodes.length; i++) {
    const n = nodes[i];
    if (n.kind === 'glue' || n.kind === 'penalty') positions.push(i + 1);
  }
  positions.push(nodes.length);
  return [...new Set(positions)].sort((a, b) => a - b);
}

export interface LineMetrics {
  text: string;
  naturalWidth: number;
  stretch: number;
  shrink: number;
  hyphenated: boolean;
  /** true when the line is a single unbreakable run (no glue/penalty inside) */
  atomic: boolean;
  hasContent: boolean;
}

/**
 * Metrics of the line spanning nodes[start .. end-1], where `end` is a break
 * position. A trailing glue contributes nothing; a trailing penalty adds its
 * width (the hyphen) and flags the line.
 */
export function measureLine(nodes: Node[], start: number, end: number): LineMetrics {
  let naturalWidth = 0;
  let stretch = 0;
  let shrink = 0;
  let hyphenated = false;
  let atomic = true;
  let hasContent = false;
  let text = '';
  for (let i = start; i < end; i++) {
    const n = nodes[i];
    const last = i === end - 1;
    if (n.kind === 'box') {
      naturalWidth += n.width;
      text += n.text;
      hasContent = true;
    } else if (n.kind === 'glue') {
      if (!last) {
        naturalWidth += n.width;
        stretch += n.stretch;
        shrink += n.shrink;
        text += ' ';
        atomic = false;
      }
    } else {
      // penalty
      atomic = false;
      if (last) {
        naturalWidth += n.width;
        hyphenated = n.flagged;
        text += n.text;
        hasContent = true;
      }
    }
  }
  return { text, naturalWidth, stretch, shrink, hyphenated, atomic, hasContent };
}
