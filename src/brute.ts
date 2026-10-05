/** 暴力枚举全排列的参照求解器, 仅用于小规模 (n <= 9) 正确性对拍 */
import { Problem } from "./scheduler";

export interface BruteResult {
  totalPenalty: number;
  makespan: number;
  order: number[];
}

export function solveBrute(problem: Problem): BruteResult {
  const jobs = problem.jobs;
  const n = jobs.length;
  const setup = problem.setup;
  const initialSetup = problem.initialSetup ?? new Array<number>(n).fill(0);
  const anticip = problem.anticipatorySetup ?? true;

  let bestP = Infinity;
  let bestT = Infinity;
  let bestOrder: number[] = [];
  const perm: number[] = [];
  const used = new Array<boolean>(n).fill(false);

  const dfs = (): void => {
    if (perm.length === n) {
      let free = 0;
      let prev = -1;
      let penalty = 0;
      for (const k of perm) {
        const s = prev < 0 ? initialSetup[k] : setup[prev][k];
        const end = anticip
          ? Math.max(free + s, jobs[k].release) + jobs[k].proc
          : Math.max(free, jobs[k].release) + s + jobs[k].proc;
        penalty += jobs[k].weight * Math.max(0, end - jobs[k].due);
        free = end;
        prev = k;
      }
      if (penalty < bestP || (penalty === bestP && free < bestT)) {
        bestP = penalty;
        bestT = free;
        bestOrder = perm.slice();
      }
      return;
    }
    for (let k = 0; k < n; k++) {
      if (used[k]) continue;
      used[k] = true;
      perm.push(k);
      dfs();
      perm.pop();
      used[k] = false;
    }
  };
  dfs();
  return { totalPenalty: bestP, makespan: bestT, order: bestOrder };
}
