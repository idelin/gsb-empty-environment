import { segmentGraphemes } from './grapheme.ts';
import type { Atom, Direction, InlineNode } from './types.ts';

/** 节点树 -> 逻辑序原子序列;同时产出完整可访问名称。 */
export function flatten(nodes: InlineNode[]): { atoms: Atom[]; accessibleName: string } {
  const atoms: Atom[] = [];
  let accessible = '';
  let offset = 0;

  const pushCluster = (text: string, dir: Direction, linkId: string | null,
    linkRange: { start: number; end: number } | null) => {
    const start = offset;
    offset += text.length;
    accessible += text;
    atoms.push({
      kind: 'cluster', text, start, end: offset, dir,
      linkId, linkRange, entityId: null,
      breakAfter: text === ' ' || text === '\t',
    });
  };

  const walk = (node: InlineNode, dir: Direction, linkId: string | null,
    linkRange: { start: number; end: number } | null) => {
    switch (node.kind) {
      case 'text': {
        for (const g of segmentGraphemes(node.text)) {
          pushCluster(g, node.dir ?? dir, linkId, linkRange);
        }
        return;
      }
      case 'link': {
        const start = offset;
        for (const child of node.children) walk(child, dir, node.linkId, null);
        const range = { start, end: offset };
        // 回填该链接区间内所有原子的 linkRange
        for (const a of atoms) {
          if (a.linkId === node.linkId && a.start >= range.start && a.end <= range.end) {
            a.linkRange = range;
          }
        }
        return;
      }
      case 'entity': {
        // 不可拆实体:整体作为一个原子,内部不切段、不可截断
        const start = offset;
        offset += node.text.length;
        accessible += node.text;
        atoms.push({
          kind: 'entity', text: node.text, start, end: offset, dir,
          linkId, linkRange, entityId: node.entityId, breakAfter: false,
        });
        return;
      }
    }
  };

  for (const n of nodes) walk(n, 'ltr', null, null);
  return { atoms, accessibleName: accessible };
}
