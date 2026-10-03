import type { Network } from './network.ts';

/**
 * 8-node multi-ring topology:
 *
 *   ring-1: N1 - N2 - N3 - N4 - N1          (all cost 1)
 *   ring-2: N5 - N6 - N7 - N8 - N5          (all cost 1)
 *   core:   N3 - N5  cost 1  (primary backbone between rings)
 *           N4 - N8  cost 2  (backup backbone)
 *   long-haul: N2 - N6 cost 5
 */
export function buildTopology(net: Network): void {
  for (const id of ['N1', 'N2', 'N3', 'N4', 'N5', 'N6', 'N7', 'N8']) net.addNode(id);
  net.addLink('N1', 'N2', 1);
  net.addLink('N2', 'N3', 1);
  net.addLink('N3', 'N4', 1);
  net.addLink('N4', 'N1', 1);
  net.addLink('N5', 'N6', 1);
  net.addLink('N6', 'N7', 1);
  net.addLink('N7', 'N8', 1);
  net.addLink('N8', 'N5', 1);
  net.addLink('N3', 'N5', 1);
  net.addLink('N4', 'N8', 2);
  net.addLink('N2', 'N6', 5);
}
