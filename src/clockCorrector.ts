import type { ServerId } from './types.ts';

/**
 * 差分约束：offset[to] - offset[from] >= minGapMs。
 * 由消息因果（发送必须先于接收）导出。
 */
export interface ClockConstraint {
  from: ServerId;
  to: ServerId;
  minGapMs: number;
  reason: string;
}

/** 约束矛盾（存在正环，物理上不可调和）时抛出。 */
export class InconsistentTraceError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'InconsistentTraceError';
  }
}

/**
 * 求解各服务器的时钟基线偏移，使所有因果约束成立。
 *
 * 约束形如 offset[v] >= offset[u] + w，是典型的差分约束系统，
 * 等价于在约束图上求以“超级源点 0”为起点的最长路。
 * 这里用 Bellman-Ford 风格的松弛求解：
 *   - 初始 dist[s] = 0（相当于超级源点到各点有 0 边），
 *     保证解是满足约束的“最小平移”，不引入多余的整体漂移；
 *   - 第 N 轮仍能松弛说明存在正环，即日志自相矛盾。
 */
export function solveClockOffsets(
  servers: Iterable<ServerId>,
  constraints: ClockConstraint[],
): Map<ServerId, number> {
  const dist = new Map<ServerId, number>();
  for (const server of servers) dist.set(server, 0);
  for (const c of constraints) {
    if (!dist.has(c.from)) dist.set(c.from, 0);
    if (!dist.has(c.to)) dist.set(c.to, 0);
  }

  const size = dist.size;
  for (let round = 0; round < size; round++) {
    let changed = false;
    for (const c of constraints) {
      const candidate = (dist.get(c.from) as number) + c.minGapMs;
      if (candidate > (dist.get(c.to) as number)) {
        if (round === size - 1) {
          throw new InconsistentTraceError(
            `时钟约束存在正环，日志自相矛盾（冲突约束: ${c.reason}，要求 ` +
              `offset[${c.to}] >= offset[${c.from}] + ${c.minGapMs}ms）`,
          );
        }
        dist.set(c.to, candidate);
        changed = true;
      }
    }
    if (!changed) break;
  }
  return dist;
}

/**
 * 由一条消息的 send/recv 事件导出时钟约束：
 *   corrected(recv) - corrected(send) >= minLatencyMs
 *   => offset[recvServer] >= offset[sendServer] + sendLocal + minLatency - recvLocal
 */
export function messageConstraint(
  sendServer: ServerId,
  sendLocalMs: number,
  recvServer: ServerId,
  recvLocalMs: number,
  minLatencyMs: number,
  reason: string,
): ClockConstraint {
  return {
    from: sendServer,
    to: recvServer,
    minGapMs: sendLocalMs + minLatencyMs - recvLocalMs,
    reason,
  };
}
