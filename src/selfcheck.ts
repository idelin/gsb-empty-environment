/** 自检: 小规模随机实例与暴力枚举对拍 + 重构一致性 + n=18 性能测试 */
import { Problem, Job, solve } from "./scheduler";
import { solveBrute } from "./brute";

let seed = 20261005;
function rand(): number {
  // xorshift32, 保证可复现
  seed ^= seed << 13; seed >>>= 0;
  seed ^= seed >> 17;
  seed ^= seed << 5; seed >>>= 0;
  return seed / 0xffffffff;
}
const ri = (lo: number, hi: number): number => lo + Math.floor(rand() * (hi - lo + 1));

function randomProblem(n: number, anticip: boolean): Problem {
  const jobs: Job[] = [];
  for (let i = 0; i < n; i++) {
    jobs.push({
      id: `J${i}`,
      release: ri(0, 25),
      proc: ri(1, 9),
      due: ri(0, 45),
      weight: ri(1, 5),
    });
  }
  const setup: number[][] = [];
  for (let i = 0; i < n; i++) {
    const row: number[] = [];
    for (let j = 0; j < n; j++) row.push(i === j ? 0 : ri(0, 7));
    setup.push(row);
  }
  const initialSetup: number[] = [];
  for (let i = 0; i < n; i++) initialSetup.push(ri(0, 4));
  return { jobs, setup, initialSetup, anticipatorySetup: anticip };
}

function check(cond: boolean, msg: string): void {
  if (!cond) {
    console.error(`FAIL: ${msg}`);
    process.exitCode = 1;
  }
}

function replayPenalty(p: Problem, order: number[]): { penalty: number; makespan: number } {
  const anticip = p.anticipatorySetup ?? true;
  const init = p.initialSetup ?? p.jobs.map(() => 0);
  let free = 0;
  let prev = -1;
  let penalty = 0;
  for (const k of order) {
    const s = prev < 0 ? init[k] : p.setup[prev][k];
    const end = anticip
      ? Math.max(free + s, p.jobs[k].release) + p.jobs[k].proc
      : Math.max(free, p.jobs[k].release) + s + p.jobs[k].proc;
    penalty += p.jobs[k].weight * Math.max(0, end - p.jobs[k].due);
    free = end;
    prev = k;
  }
  return { penalty, makespan: free };
}

// 1) 对拍: n = 2..8, 两种换线模式
let cases = 0;
for (let iter = 0; iter < 400; iter++) {
  const n = ri(2, 8);
  const anticip = iter % 2 === 0;
  const prob = randomProblem(n, anticip);
  const fast = solve(prob);
  const slow = solveBrute(prob);
  check(
    fast.totalPenalty === slow.totalPenalty,
    `iter=${iter} n=${n} anticip=${anticip} 罚金不一致 dp=${fast.totalPenalty} brute=${slow.totalPenalty}`
  );
  check(
    fast.makespan === slow.makespan,
    `iter=${iter} n=${n} anticip=${anticip} makespan不一致 dp=${fast.makespan} brute=${slow.makespan}`
  );
  // 重构一致性: 段明细重放的罚金/完工必须与 DP 报告一致
  const segPenalty = fast.segments.reduce((acc, s) => acc + s.penalty, 0);
  check(segPenalty === fast.totalPenalty, `iter=${iter} 段罚金合计不一致`);
  check(
    fast.segments[fast.segments.length - 1].end === fast.makespan,
    `iter=${iter} 段完工与makespan不一致`
  );
  const replay = replayPenalty(prob, fast.order);
  check(
    replay.penalty === fast.totalPenalty && replay.makespan === fast.makespan,
    `iter=${iter} 顺序重放不一致`
  );
  cases++;
}
console.log(`对拍通过: ${cases} 个随机实例 (n=2..8, 含抢占/非抢占换线两种模式)`);

// 2) 性能: n = 18
const big = randomProblem(18, true);
const t0 = Date.now();
const solBig = solve(big);
const ms = Date.now() - t0;
console.log(
  `n=18 求解: ${ms} ms, 状态数=${solBig.statesExplored}, 罚金=${solBig.totalPenalty}, makespan=${solBig.makespan}`
);
check(ms < 120000, "n=18 求解超时");

if (process.exitCode) {
  console.error("自检失败");
} else {
  console.log("全部自检通过");
}
