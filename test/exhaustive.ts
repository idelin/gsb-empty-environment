// 穷举核验：枚举 n <= 4 的全部有向图（含/不含自环），
// 对每个容量 1..4 比较精确解与暴力解，并独立校验计划与见证。

import { bruteForceMinBatches } from '../src/brute';
import type { PlanRequest } from '../src/model';
import { checkInstance, componentsOf } from './helpers';

function buildRequest(n: number, edgeMask: number, allowSelfLoop: boolean, capacity: number): PlanRequest {
  const projects = [];
  for (let i = 0; i < n; i++) projects.push({ id: 'p' + i, deps: [] as string[] });
  let bit = 0;
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (!allowSelfLoop && i === j) continue;
      if ((edgeMask & (1 << bit)) !== 0) projects[i].deps.push('p' + j);
      bit++;
    }
  }
  return { capacity, projects };
}

export function run(): void {
  let checked = 0;
  const t0 = Date.now();

  // n = 1..4，不含自环：边数 n*(n-1)
  for (let n = 1; n <= 4; n++) {
    const edgeCount = n * (n - 1);
    const total = 1 << edgeCount;
    for (let mask = 0; mask < total; mask++) {
      for (let cap = 1; cap <= 4; cap++) {
        const req = buildRequest(n, mask, false, cap);
        const comp = componentsOf(req);
        const brute = bruteForceMinBatches(comp.sizes, comp.predMask, cap);
        checkInstance(req, brute, `n=${n} mask=${mask} cap=${cap}`);
        checked++;
      }
    }
  }

  // n = 1..3，含自环：边数 n*n
  for (let n = 1; n <= 3; n++) {
    const edgeCount = n * n;
    const total = 1 << edgeCount;
    for (let mask = 0; mask < total; mask++) {
      for (let cap = 1; cap <= 4; cap++) {
        const req = buildRequest(n, mask, true, cap);
        const comp = componentsOf(req);
        const brute = bruteForceMinBatches(comp.sizes, comp.predMask, cap);
        checkInstance(req, brute, `self n=${n} mask=${mask} cap=${cap}`);
        checked++;
      }
    }
  }

  console.log(`exhaustive: ${checked} 个实例全部通过（${Date.now() - t0}ms）`);
}
