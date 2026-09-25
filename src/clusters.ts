import type { Cluster, Level, Metrics, TreeNode } from './types.ts';
import { dirOf, isTextNode } from './types.ts';

interface FlatRun {
  nodeId: string;
  text: string;
  level: Level;
}

/** Depth-first flattening of the node tree with span-level inheritance. */
export function flattenTree(root: TreeNode): FlatRun[] {
  const runs: FlatRun[] = [];
  const visit = (node: TreeNode, inherited: Level | undefined): void => {
    if (isTextNode(node)) {
      const level = node.level ?? inherited;
      if (level === undefined) {
        throw new Error(`text node "${node.id}" has no explicit level and no span level to inherit`);
      }
      runs.push({ nodeId: node.id, text: node.text, level });
      return;
    }
    const spanLevel = node.level ?? inherited;
    for (const child of node.children) visit(child, spanLevel);
  };
  visit(root, undefined);
  return runs;
}

/**
 * Segment runs into grapheme clusters. Intl.Segmenter is only used as the
 * cluster splitter (a Unicode utility), never as a layout engine: all
 * geometry and ordering is computed in this package. Newline characters
 * mark forced paragraph breaks and carry no width.
 */
const segmenter = new Intl.Segmenter('en', { granularity: 'grapheme' });

export function buildClusters(runs: readonly FlatRun[], metrics: Metrics): Cluster[] {
  const clusters: Cluster[] = [];
  let index = 0;
  let codeUnitOffset = 0;
  for (const run of runs) {
    for (const seg of segmenter.segment(run.text)) {
      const text = seg.segment;
      const hardBreak = text === '\n';
      clusters.push({
        index,
        text,
        level: run.level,
        dir: dirOf(run.level),
        nodeId: run.nodeId,
        codeUnitOffset,
        width: hardBreak ? 0 : metrics.widthOf(text, run.nodeId),
        hardBreak,
      });
      index += 1;
      codeUnitOffset += text.length;
    }
  }
  return clusters;
}
