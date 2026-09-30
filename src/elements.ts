import type { Paragraph } from './model.ts';

/** 把节点树展开为 盒/胶/罚点 元素流 (Knuth-Plass 风格的线性化)。 */
export interface BoxElement {
  kind: 'box';
  width: number;
  text: string;
}
export interface GlueElement {
  kind: 'glue';
  width: number;
  stretch: number;
  shrink: number;
}
export interface PenaltyElement {
  kind: 'penalty';
  width: number; // 断在此处时行尾连字符的宽度
  penalty: number;
  flagged: boolean; // 断词点标记
  text: string;
}
export type Element = BoxElement | GlueElement | PenaltyElement;

export function toElements(paragraph: Paragraph, hyphenWidth = 5): Element[] {
  const nodes = paragraph.nodes;
  if (nodes.length === 0) throw new Error('空段落');
  if (nodes[0].kind !== 'word' || nodes[nodes.length - 1].kind !== 'word') {
    throw new Error('段落必须以词开始并以词结束');
  }
  const out: Element[] = [];
  for (const node of nodes) {
    if (node.kind === 'space') {
      const prev = out[out.length - 1];
      if (prev && prev.kind === 'glue') throw new Error('不允许相邻空格');
      out.push({ kind: 'glue', width: node.width, stretch: node.stretch, shrink: node.shrink });
    } else {
      node.fragments.forEach((frag, i) => {
        out.push({ kind: 'box', width: frag.width, text: frag.text });
        if (i < node.fragments.length - 1) {
          out.push({
            kind: 'penalty',
            width: hyphenWidth,
            penalty: node.hyphenPenalties[i],
            flagged: true,
            text: '-',
          });
        }
      });
    }
  }
  return out;
}

/** 所有合法断点: 胶之后 / 罚点处, 以及段尾 (elements.length)。 */
export function breakPositions(elements: Element[]): number[] {
  const positions: number[] = [];
  for (let i = 0; i < elements.length; i++) {
    const kind = elements[i].kind;
    if (kind === 'glue' || kind === 'penalty') positions.push(i);
  }
  positions.push(elements.length);
  return positions;
}

export function isHyphenBreak(elements: Element[], position: number): boolean {
  return position < elements.length && elements[position].kind === 'penalty';
}
