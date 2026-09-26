/** Deterministic fixtures: the node tree and the glyph metrics used by
 *  every test and by the demo. Metrics stand in for real text measurement
 *  so core computations never touch a browser layout engine. */
import type { GlyphMetrics } from './layout.ts';
import type { NodeId, Tree, TreeNode } from './types.ts';

export const METRICS: GlyphMetrics = { charWidth: 8, rowHeight: 20, indentWidth: 16 };

function node(id: NodeId, parentId: NodeId | null, label: string, children: NodeId[] = []): TreeNode {
  return { id, parentId, label, children };
}

export function makeTree(): Tree {
  const nodes: Record<NodeId, TreeNode> = {
    root: node('root', null, '', ['fruits', 'veggies', 'grains']),
    fruits: node('fruits', 'root', 'Fruits', ['apple', 'banana', 'cherry']),
    apple: node('apple', 'fruits', 'Apple'),
    banana: node('banana', 'fruits', 'Banana', ['plantain', 'cavendish']),
    plantain: node('plantain', 'banana', 'Plantain'),
    cavendish: node('cavendish', 'banana', 'Cavendish'),
    cherry: node('cherry', 'fruits', 'Cherry'),
    veggies: node('veggies', 'root', 'Veggies', ['carrot', 'daikon']),
    carrot: node('carrot', 'veggies', 'Carrot'),
    daikon: node('daikon', 'veggies', 'Daikon'),
    grains: node('grains', 'root', 'Grains', ['rice']),
    rice: node('rice', 'grains', 'Rice'),
  };
  return { rootId: 'root', nodes };
}
