/**
 * 自测：随机小算例（n <= 8）上，用全排列暴力枚举与 DP 结果对拍。
 * 运行：node src/selftest.ts [轮数]
 */
import { solve, type Problem } from "./scheduler.ts";

interface EvalResult {
  cost: number;
  time: number;
}

const evaluate = (problem: Problem, order: number[]): EvalResult => {
  const { jobs, setup } = problem;
  const initialSetup = problem.initialSetup ?? jobs.map(() => 0);
  let time = 0;
  let cost = 0;
  let prev = -1;
  for (const j of order) {
    const ready = time + (prev < 0 ? initialSetup[j] : setup[prev][j]);
    const start = Math.max(ready, jobs[j].releaseTime);
    time = start + jobs[j].processingTime;
    cost += jobs[j].weight * Math.max(0, time - jobs[j].dueDate);
    prev = j;
  }
  return { cost, time };
};

const bruteForce = (problem: Problem): EvalResult => {
  const n = problem.jobs.length;
  const order = Array.from({ length: n }, (_, i) => i);
  let best: EvalResult = { cost: Infinity, time: Infinity };
  const visit = (k: number): void => {
    if (k === n) {
      const cur = evaluate(problem, order);
      if (cur.cost < best.cost || (cur.cost === best.cost && cur.time < best.time)) {
        best = cur;
      }
      return;
    }
    for (let i = k; i < n; i++) {
      [order[k], order[i]] = [order[i], order[k]];
      visit(k + 1);
      [order[k], order[i]] = [order[i], order[k]];
    }
  };
  visit(0);
  return best;
};

// 可复现的伪随机数（mulberry32）
const rng = (seed: number) => () => {
  seed |= 0;
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

const randomProblem = (rand: () => number, n: number): Problem => {
  const int = (lo: number, hi: number) => lo + Math.floor(rand() * (hi - lo + 1));
  const jobs = Array.from({ length: n }, (_, i) => ({
    id: `J${i}`,
    processingTime: int(1, 8),
    releaseTime: int(0, 15),
    dueDate: int(4, 30),
    weight: int(1, 6),
  }));
  const setup = Array.from({ length: n }, (_, i) =>
    Array.from({ length: n }, (_, j) => (i === j ? 0 : int(0, 6))),
  );
  const initialSetup = Array.from({ length: n }, () => int(0, 3));
  return { jobs, setup, initialSetup };
};

const rounds = Number(process.argv[2] ?? 300);
const rand = rng(20261005);
let checked = 0;
for (let round = 0; round < rounds; round++) {
  const n = 2 + Math.floor(rand() * 7); // 2..8
  const problem = randomProblem(rand, n);
  const expected = bruteForce(problem);
  const got = solve(problem);
  if (got.totalPenalty !== expected.cost || got.completionTime !== expected.time) {
    console.error("对拍失败！", JSON.stringify(problem));
    console.error(`暴力: cost=${expected.cost} time=${expected.time}`);
    console.error(`DP  : cost=${got.totalPenalty} time=${got.completionTime}`);
    process.exit(1);
  }
  // 校验输出分段自洽：换线/等待/开工/完工与罚金一致
  let time = 0;
  let prev = -1;
  let cost = 0;
  for (const seg of got.segments) {
    const job = problem.jobs[seg.jobIndex];
    const expectedSetup = prev < 0 ? (problem.initialSetup ?? [])[seg.jobIndex] ?? 0 : problem.setup[prev][seg.jobIndex];
    if (seg.setupTime !== expectedSetup) throw new Error("换线时间不一致");
    if (seg.startTime !== time + seg.setupTime + seg.waitTime) throw new Error("时间轴不连续");
    if (seg.startTime < job.releaseTime) throw new Error("违反释放时间");
    if (seg.endTime !== seg.startTime + job.processingTime) throw new Error("完工时刻错误");
    if (seg.penalty !== job.weight * Math.max(0, seg.endTime - job.dueDate)) {
      throw new Error("罚金错误");
    }
    cost += seg.penalty;
    time = seg.endTime;
    prev = seg.jobIndex;
  }
  if (cost !== got.totalPenalty || time !== got.completionTime) {
    throw new Error("汇总不一致");
  }
  checked++;
}
console.log(`全部通过：${checked} 个随机算例，DP 与暴力枚举结果一致。`);
