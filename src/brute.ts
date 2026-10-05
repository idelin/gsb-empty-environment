import type { PlanInput } from './types.ts';

/**
 * 穷举核验器：与 planner.ts 完全独立的实现，用于小例子交叉验证。
 *
 * 直接在项目级别枚举“每个项目分到哪一批”，约束：
 *  - 若 u 依赖 v 且二者互不可达（不同依赖环）：batch[v] < batch[u]；
 *  - 若 u 依赖 v 且二者互达（同一依赖环）：batch[v] ≤ batch[u]（环上项目被迫同批）；
 *  - 每批项目数 ≤ capacity。
 * 返回最少批次数；任何批次数都不可行时返回 null。
 */
export function bruteForceMinBatches(input: PlanInput): number | null {
  const n = input.projects.length;
  if (n === 0) return 0;
  const capacity = input.capacity;
  const indexOf = new Map(input.projects.map((p, i) => [p.name, i]));
  const prereqsOf: number[][] = input.projects.map(() => []);
  input.projects.forEach((p, i) => {
    for (const dep of p.dependsOn ?? []) prereqsOf[i].push(indexOf.get(dep) as number);
  });

  // 互达关系（独立实现：每个点 DFS 求可达集）
  const reachable: boolean[][] = Array.from({ length: n }, () => new Array(n).fill(false));
  for (let s = 0; s < n; s++) {
    const stack = [s];
    while (stack.length > 0) {
      const u = stack.pop() as number;
      for (const v of prereqsOf[u]) {
        if (!reachable[s][v]) {
          reachable[s][v] = true;
          stack.push(v);
        }
      }
    }
  }

  // 逐条直接依赖边的约束：strict[u][v] = true 表示 batch[v] 必须严格小于 batch[u]
  const edges: Array<{ u: number; v: number; strict: boolean }> = [];
  for (let u = 0; u < n; u++) {
    for (const v of prereqsOf[u]) {
      const sameCycle = reachable[u][v] && reachable[v][u];
      edges.push({ u, v, strict: !sameCycle });
    }
  }

  const batchOf = new Int32Array(n).fill(-1);
  const load = new Int32Array(n); // 每个批次的当前项目数

  const feasible = (k: number): boolean => {
    // 注意：批次是有序的（严格先后约束），不能按“编号 ≤ 已用+1”做对称性剪枝。
    const assign = (i: number): boolean => {
      if (i === n) return true;
      for (let b = 1; b <= k; b++) {
        if (load[b - 1] >= capacity) continue;
        let ok = true;
        for (const { u, v, strict } of edges) {
          if (u !== i && v !== i) continue;
          const bu = u === i ? b : batchOf[u];
          const bv = v === i ? b : batchOf[v];
          if (bu < 1 || bv < 1) continue;
          if (strict ? !(bv < bu) : !(bv <= bu)) {
            ok = false;
            break;
          }
        }
        if (!ok) continue;
        batchOf[i] = b;
        load[b - 1]++;
        if (assign(i + 1)) return true;
        batchOf[i] = -1;
        load[b - 1]--;
      }
      return false;
    };
    batchOf.fill(-1);
    load.fill(0);
    return assign(0);
  };

  for (let k = 1; k <= n; k++) {
    if (feasible(k)) return k;
  }
  return null;
}
