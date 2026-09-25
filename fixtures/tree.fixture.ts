import type { NodeData } from '../src/types.ts';

/** Deterministic fixture tree: mixed enabled/disabled, one lazy branch. */
export const fixtureTree: NodeData[] = [
  {
    id: 'alpha',
    label: 'alpha',
    children: [
      { id: 'alpha-one', label: 'alpha one' },
      { id: 'alpha-two', label: 'alpha two', disabled: true },
      { id: 'alpha-three', label: 'alpha three', children: [{ id: 'alpha-three-x', label: 'alpha three x' }] },
    ],
  },
  {
    id: 'beta',
    label: 'beta',
    disabled: true,
    children: [{ id: 'beta-one', label: 'beta one' }],
  },
  {
    id: 'gamma',
    label: 'gamma',
    children: [
      { id: 'gamma-one', label: 'gamma one' },
      { id: 'gamma-two', label: 'gamma two' },
    ],
  },
  { id: 'remote', label: 'remote', hasChildren: true },
  { id: 'delta', label: 'delta' },
];

/** Children returned by the async loader for the lazy `remote` branch. */
export const remoteChildren: NodeData[] = [
  { id: 'remote-1', label: 'remote one' },
  { id: 'remote-2', label: 'remote two', disabled: true },
  { id: 'remote-3', label: 'remote three' },
];

/** Controllable async loader over a static id -> children map. */
export function fixtureLoader(map: Record<string, NodeData[]>, delayMs = 0) {
  return (id: string): Promise<NodeData[]> =>
    new Promise((resolve, reject) => {
      const children = map[id];
      if (children === undefined) {
        reject(new Error(`no fixture children for ${id}`));
        return;
      }
      if (delayMs <= 0) resolve(children);
      else setTimeout(() => resolve(children), delayMs);
    });
}
