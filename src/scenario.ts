import { Simulator } from './simulator.ts';
import type { LeaseConfig } from './types.ts';

/** 验收场景参数:3 节点,多数派 = 2。 */
export const ACCEPTANCE_CONFIG: LeaseConfig = {
  nodeIds: ['A', 'B', 'C'],
  leaseTtl: 50,
  maxClockSkew: 5,
  networkDelay: 5,
  heartbeatInterval: 10,
  electionRetryInterval: 10,
  electionBackoffUnit: 10,
};

export const SCENARIO_END_TICK = 180;

/**
 * 验收时间线:
 * - tick 100: A 发起租约请求
 * - tick 105: 多数派确认,A 成为主(租约 [105,155],保守降级点 150)
 * - tick 106: 注入网络黑洞,隔离 A
 * - tick 149: 客户端写 A -> 提交(租约仍有效)
 * - tick 150: A 租约保守到期,未获多数派续期 -> 主动降级为候选者;B 发起选举
 * - tick 151: 客户端写 A -> 拒绝(已非主)
 * - tick 155: B 获多数派确认成为新主(旧租约恰已过期)
 * - tick 156: 客户端写 B -> 提交
 * - tick 160: 解除黑洞;旧主 A 的重试请求在 165 被多数派以 LEASE_HELD 拒绝
 * - tick 170: 客户端写 A -> 拒绝;B 心跳到达,A 转为跟随者
 */
export function buildAcceptanceScenario(): Simulator {
  const sim = new Simulator(ACCEPTANCE_CONFIG);
  sim.at(100, () => sim.startElection('A'));
  sim.at(106, () => {
    sim.network.isolate('A');
    sim.log(null, 'BLACKHOLE_ON', 'node=A');
  });
  sim.at(149, () => sim.clientWrite('A', 'w1'));
  sim.at(151, () => sim.clientWrite('A', 'w2'));
  sim.at(156, () => sim.clientWrite('B', 'w3'));
  sim.at(160, () => {
    sim.network.heal('A');
    sim.log(null, 'BLACKHOLE_OFF', 'node=A');
  });
  sim.at(170, () => sim.clientWrite('A', 'w4'));
  return sim;
}

export function runAcceptanceScenario(): Simulator {
  const sim = buildAcceptanceScenario();
  sim.runUntil(SCENARIO_END_TICK);
  return sim;
}
