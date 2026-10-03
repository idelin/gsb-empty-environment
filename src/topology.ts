import { Network } from './network.ts';
import { DEFAULT_DAMPENING, type DampeningConfig } from './dampening.ts';

/**
 * 8 节点多环拓扑：
 *
 *     环A                       环B
 *   R1 ---- R2                R5 ---- R6
 *   |       |                 |       |
 *   |       |                 |       |
 *   R4 ---- R3 == R3-R5(2)== R5      |
 *   |       |                 |       |
 *   |       +== R4-R8(3) ==== R8 ---- R7
 *   （R4-R8 为备用主干，直接连 R4 与 R8）
 *
 * 环A: R1-R2-R3-R4-R1 (cost=1)
 * 环B: R5-R6-R7-R8-R5 (cost=1)
 * 主干: R3-R5 (cost=2, 核心), R4-R8 (cost=3, 备份)
 */
export function buildMultiRingTopology(
  dampCfg: DampeningConfig = DEFAULT_DAMPENING,
): Network {
  const net = new Network(dampCfg);
  for (const id of ['R1', 'R2', 'R3', 'R4', 'R5', 'R6', 'R7', 'R8']) {
    net.addRouter(id);
  }
  // 环 A
  net.addLink('R1', 'R2', 1);
  net.addLink('R2', 'R3', 1);
  net.addLink('R3', 'R4', 1);
  net.addLink('R4', 'R1', 1);
  // 环 B
  net.addLink('R5', 'R6', 1);
  net.addLink('R6', 'R7', 1);
  net.addLink('R7', 'R8', 1);
  net.addLink('R8', 'R5', 1);
  // 双主干互联（多环）
  net.addLink('R3', 'R5', 2); // 核心主干
  net.addLink('R4', 'R8', 3); // 备用主干
  return net;
}
