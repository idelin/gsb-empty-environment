/**
 * 单机生产排程器（精确求解，最多 18 个任务）
 *
 * 问题特征：
 *  - 换线时间有方向性：setup[i][j] !== setup[j][i]（序列相关换线）
 *  - 每个任务有释放时间 releaseTime、期限 dueDate、单位迟交罚金 weight
 *  - 主目标：最小化总迟交罚金  sum( weight_j * max(0, C_j - dueDate_j) )
 *  - 次目标：在主目标最优的前提下，最小化最终完工时间（makespan）
 *  - 允许主动空闲：输出中每段显式给出等待时间
 *
 * 算法：Held-Karp 子集 DP。
 *  状态 = (已排任务集合 S, 最后一个任务 last)。
 *  同一状态可能有多条 (累计罚金, 当前时刻) 都不可互相替代的“帕累托标签”，
 *  例如罚金低但时刻晚的标签，可能让后续任务更迟、罚金更高，因此不能
 *  只保留单一最优，必须保留帕累托前沿（cost、time 互不支配）。
 *
 * 关于主动空闲的说明：
 *  总加权迟交罚金与 makespan 都是“正则目标”（对完工时间单调不减），
 *  因此存在最优调度，其空闲仅发生在“下一任务尚未释放”时；
 *  任何额外主动插入的空闲都会产生一个被支配的标签（同集合、同末尾、
 *  时刻更晚、罚金不降），不可能导向更优解。DP 遍历全部排列并记录
 *  由释放时间强制产生的等待段，故不需要、也不会默认“可执行就立即开工”
 *  之外的任何启发式——最优性由穷举保证。
 */

export interface Job {
  id: string;
  /** 加工时间 */
  processingTime: number;
  /** 释放时间（最早可开工时刻） */
  releaseTime: number;
  /** 交期 */
  dueDate: number;
  /** 单位迟交罚金（罚金 = weight * max(0, 完工时刻 - 交期)） */
  weight: number;
}

export interface Problem {
  jobs: Job[];
  /**
   * setup[i][j]：从任务 i 换线到任务 j 的时间（有方向，允许不对称）。
   * 维度为 n x n，对角线值忽略。
   */
  setup: number[][];
  /** 可选：开机后首个任务 j 之前的初始换线时间，默认全 0 */
  initialSetup?: number[];
}

export interface Segment {
  seq: number;
  jobIndex: number;
  jobId: string;
  /** 本段开工前的换线时间 */
  setupTime: number;
  /** 本段开工前的等待（空闲）时间 */
  waitTime: number;
  startTime: number;
  endTime: number;
  dueDate: number;
  tardiness: number;
  penalty: number;
}

export interface Solution {
  totalPenalty: number;
  completionTime: number;
  segments: Segment[];
}

export function solve(problem: Problem): Solution {
  const { jobs, setup } = problem;
  const n = jobs.length;
  if (n === 0) return { totalPenalty: 0, completionTime: 0, segments: [] };
  if (n > 18) throw new Error(`任务数 ${n} 超过上限 18`);
  if (setup.length !== n || setup.some((row) => row.length !== n)) {
    throw new Error("setup 矩阵维度必须为 n x n");
  }
  const initialSetup = problem.initialSetup ?? new Array<number>(n).fill(0);
  if (initialSetup.length !== n) throw new Error("initialSetup 长度必须为 n");

  const fullMask = (1 << n) - 1;
  // 标签只存平行数组：cost/time/parent/job，回溯所需信息均可由父链重建，
  // 且支配检查在分配标签之前完成，避免为被支配的转移分配内存。
  const labelCost: number[] = [];
  const labelTime: number[] = [];
  const labelParent: number[] = [];
  const labelJob: number[] = [];
  // frontiers[key]，key = subset * n + last，存放帕累托标签下标
  const frontiers: (number[] | undefined)[] = new Array(n * (1 << n));

  const pushLabel = (key: number, cost: number, time: number, parent: number, job: number): void => {
    const idx = labelCost.length;
    let frontier = frontiers[key];
    if (frontier === undefined) {
      labelCost.push(cost);
      labelTime.push(time);
      labelParent.push(parent);
      labelJob.push(job);
      frontiers[key] = [idx];
      return;
    }
    // 若已有标签支配新标签（罚金不高且时刻不晚），丢弃
    for (const i of frontier) {
      if (labelCost[i] <= cost && labelTime[i] <= time) return;
    }
    // 移除被新标签支配的旧标签
    let w = 0;
    for (const i of frontier) {
      if (cost <= labelCost[i] && time <= labelTime[i]) continue;
      frontier[w++] = i;
    }
    frontier.length = w;
    labelCost.push(cost);
    labelTime.push(time);
    labelParent.push(parent);
    labelJob.push(job);
    frontier.push(idx);
  };

  // 初始：空集合 -> 第一个任务
  for (let j = 0; j < n; j++) {
    const job = jobs[j];
    const start = Math.max(initialSetup[j], job.releaseTime);
    const end = start + job.processingTime;
    pushLabel((1 << j) * n + j, job.weight * Math.max(0, end - job.dueDate), end, -1, j);
  }

  // 子集 DP：按子集递增枚举，向未包含的任务扩展
  for (let subset = 1; subset <= fullMask; subset++) {
    if (subset === fullMask) break; // 全集无可扩展任务，且其前沿要留给终点选择
    const base = subset * n;
    for (let last = 0; last < n; last++) {
      const frontier = frontiers[base + last];
      if (frontier === undefined) continue;
      const setupFromLast = setup[last];
      for (let next = 0; next < n; next++) {
        if (subset & (1 << next)) continue;
        const job = jobs[next];
        const setupTime = setupFromLast[next];
        const nextKey = (subset | (1 << next)) * n + next;
        for (const li of frontier) {
          const ready = labelTime[li] + setupTime;
          const start = Math.max(ready, job.releaseTime);
          const end = start + job.processingTime;
          pushLabel(
            nextKey,
            labelCost[li] + job.weight * Math.max(0, end - job.dueDate),
            end,
            li,
            next,
          );
        }
      }
      // 该状态已扩展完毕，前沿不再被读取（回溯走父指针），释放内存
      frontiers[base + last] = undefined;
    }
  }

  // 终点：全集上取 (罚金, 完工时刻) 字典序最优的标签
  let best = -1;
  const fullBase = fullMask * n;
  for (let last = 0; last < n; last++) {
    const frontier = frontiers[fullBase + last];
    if (frontier === undefined) continue;
    for (const li of frontier) {
      if (
        best < 0 ||
        labelCost[li] < labelCost[best] ||
        (labelCost[li] === labelCost[best] && labelTime[li] < labelTime[best])
      ) {
        best = li;
      }
    }
  }
  if (best < 0) throw new Error("无可行调度");

  // 回溯：沿父指针还原，换线/等待由前后标签重建
  const chain: number[] = [];
  let cursor = best;
  while (cursor >= 0) {
    chain.push(cursor);
    cursor = labelParent[cursor];
  }
  chain.reverse();

  const segments: Segment[] = chain.map((li, i) => {
    const jobIndex = labelJob[li];
    const job = jobs[jobIndex];
    const end = labelTime[li];
    const start = end - job.processingTime;
    const setupTime =
      i === 0 ? initialSetup[jobIndex] : setup[labelJob[chain[i - 1]]][jobIndex];
    const prevEnd = i === 0 ? 0 : labelTime[chain[i - 1]];
    const waitTime = start - prevEnd - setupTime;
    const tardiness = Math.max(0, end - job.dueDate);
    return {
      seq: i + 1,
      jobIndex,
      jobId: job.id,
      setupTime,
      waitTime,
      startTime: start,
      endTime: end,
      dueDate: job.dueDate,
      tardiness,
      penalty: job.weight * tardiness,
    };
  });

  return {
    totalPenalty: labelCost[best],
    completionTime: labelTime[best],
    segments,
  };
}
