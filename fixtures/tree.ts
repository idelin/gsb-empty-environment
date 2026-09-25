import type { NodeSpec } from '../src/layout.ts';

/** Node-tree fixture: a small document used by layout tests and the demo. */
export const treeFixture: NodeSpec = {
  type: 'box',
  id: 'root',
  padding: 10,
  gap: 6,
  children: [
    { type: 'text', id: 'title', text: 'Hello world' },
    {
      type: 'box',
      id: 'card',
      padding: 4,
      gap: 2,
      children: [
        { type: 'text', id: 'body', text: 'Nested content here' },
        { type: 'text', id: 'foot', text: 'Second line' },
      ],
    },
  ],
};
