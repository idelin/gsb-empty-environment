import { planRelease } from '../src/planner.ts';
import { bruteForceMinBatches } from '../src/brute.ts';
import type { PlanInput, PlanResult } from '../src/types.ts';

let failures = 0;
function check(condition: boolean, label: string): void {
  if (!condition) {
    failures++;
    console.error(`FAIL: ${label}`);
  }
}

/** 独立的计划合法性校验（不复用求解器代码）：分区、容量、依赖顺序。 */
function validatePlan(input: PlanInput, result: PlanResult): string | null {
  if (!result.ok) return 'expected ok';
  const n = input.projects.length;
  const indexOf = new Map(input.projects.map((p, i) => [p.name, i]));
  const prereqsOf: number[][] = input.projects.map(() => []);
  input.projects.forEach((p, i) => {
    for (const dep of p.dependsOn ?? []) prereqsOf[i].push(indexOf.get(dep) as number);
  });
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
  const batchOf = new Map<string, number>();
  for (const batch of result.batches) {
    if (batch.projects.length > input.capacity) return `batch ${batch.index} exceeds capacity`;
    for (const name of batch.projects) {
      if (batchOf.has(name)) return `${name} appears twice`;
      batchOf.set(name, batch.index);
    }
  }
  if (batchOf.size !== n) return 'not every project scheduled exactly once';
  if (result.batchCount !== result.batches.length) return 'batchCount mismatch';
  result.batches.forEach((b, i) => {
    if (b.index !== i + 1) throw new Error('batch indexes not contiguous');
  });
  for (let u = 0; u < n; u++) {
    for (const v of prereqsOf[u]) {
      const bu = batchOf.get(input.projects[u].name) as number;
      const bv = batchOf.get(input.projects[v].name) as number;
      const sameCycle = reachable[u][v] && reachable[v][u];
      if (sameCycle ? bv > bu : bv >= bu) {
        return `edge ${input.projects[u].name}->${input.projects[v].name} violated (${bv} vs ${bu})`;
      }
    }
  }
  return null;
}

/** 确定性用例 */
function unitTests(): void {
  // 1. 纯链：5 个项目依次依赖，容量再大也要 5 批
  const chain: PlanInput = {
    capacity: 18,
    projects: [
      { name: 'a' },
      { name: 'b', dependsOn: ['a'] },
      { name: 'c', dependsOn: ['b'] },
      { name: 'd', dependsOn: ['c'] },
      { name: 'e', dependsOn: ['d'] },
    ],
  };
  const chainResult = planRelease(chain);
  check(chainResult.ok && chainResult.batchCount === 5, 'chain of 5 needs 5 batches');

  // 2. 18 个互不依赖的项目，容量 5 → ceil(18/5) = 4 批
  const independent: PlanInput = {
    capacity: 5,
    projects: Array.from({ length: 18 }, (_, i) => ({ name: `p${i}` })),
  };
  const indResult = planRelease(independent);
  check(indResult.ok && indResult.batchCount === 4, '18 independent / cap 5 -> 4 batches');

  // 3. 三元环容量 2 → 不可行，见证应包含全部 3 个成员与一条环
  const cycle: PlanInput = {
    capacity: 2,
    projects: [
      { name: 'x', dependsOn: ['y'] },
      { name: 'y', dependsOn: ['z'] },
      { name: 'z', dependsOn: ['x'] },
    ],
  };
  const cycleResult = planRelease(cycle);
  check(!cycleResult.ok, '3-cycle with capacity 2 is infeasible');
  if (!cycleResult.ok) {
    check(cycleResult.witness.members.length === 3, 'witness lists all 3 cycle members');
    check(
      cycleResult.witness.cycle.length === 4 &&
        cycleResult.witness.cycle[0] === cycleResult.witness.cycle[3],
      'witness contains a concrete cycle path',
    );
  }

  // 4. 二元环 + 链：环必须同批，且排在 core 之后
  const mixed: PlanInput = {
    capacity: 3,
    projects: [
      { name: 'core' },
      { name: 'pa', dependsOn: ['pb'] },
      { name: 'pb', dependsOn: ['pa', 'core'] },
    ],
  };
  const mixedResult = planRelease(mixed);
  check(mixedResult.ok && mixedResult.batchCount === 2, 'cycle after core -> 2 batches');
  if (mixedResult.ok) {
    const second = mixedResult.batches[1];
    check(
      second.projects.includes('pa') && second.projects.includes('pb'),
      'cycle members released together',
    );
    check(
      second.reasons.some((r) => r.includes('依赖环')),
      'cycle reason reported',
    );
  }

  // 5. 菱形依赖，容量 2：a → {b,c} → d，需要 3 批（b、c 同批）
  const diamond: PlanInput = {
    capacity: 2,
    projects: [
      { name: 'a' },
      { name: 'b', dependsOn: ['a'] },
      { name: 'c', dependsOn: ['a'] },
      { name: 'd', dependsOn: ['b', 'c'] },
    ],
  };
  const diamondResult = planRelease(diamond);
  check(diamondResult.ok && diamondResult.batchCount === 3, 'diamond / cap 2 -> 3 batches');
}

/** 可复现的伪随机数生成器 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomInput(rand: () => number, n: number): PlanInput {
  const projects = Array.from({ length: n }, (_, i) => ({ name: `p${i}`, dependsOn: [] as string[] }));
  const edgeProb = 0.15 + rand() * 0.35;
  for (let i = 0; i < n; i++) {
    for (let j = 0; j < n; j++) {
      if (i !== j && rand() < edgeProb) projects[i].dependsOn.push(`p${j}`);
    }
  }
  return { capacity: 1 + Math.floor(rand() * 4), projects };
}

/** 随机小例：求解器 vs 穷举 */
function randomizedCrossCheck(iterations: number): void {
  for (let seed = 1; seed <= iterations; seed++) {
    const rand = mulberry32(seed);
    const n = 1 + Math.floor(rand() * 7); // 1..7 个项目
    const input = randomInput(rand, n);
    const brute = bruteForceMinBatches(input);
    const result = planRelease(input);
    if (brute === null) {
      check(!result.ok, `seed ${seed}: solver should report infeasible`);
      if (!result.ok) {
        check(
          result.witness.size > input.capacity,
          `seed ${seed}: witness cycle must exceed capacity`,
        );
      }
    } else {
      if (!result.ok) {
        check(false, `seed ${seed}: solver reports infeasible but brute found ${brute} batches`);
      } else {
        const problem = validatePlan(input, result);
        check(problem === null, `seed ${seed}: invalid plan: ${problem ?? ''}`);
        check(
          result.batchCount === brute,
          `seed ${seed}: solver ${result.batchCount} batches, brute ${brute}`,
        );
      }
    }
  }
  console.log(`randomized cross-check: ${iterations} cases done`);
}

/** 18 个项目的性能 sanity check（含最坏情形：全独立、容量减半） */
function perfTests(): void {
  const worst: PlanInput = {
    capacity: 9,
    projects: Array.from({ length: 18 }, (_, i) => ({ name: `w${i}` })),
  };
  let start = Date.now();
  const worstResult = planRelease(worst);
  const worstMs = Date.now() - start;
  check(worstResult.ok && worstResult.batchCount === 2, '18 independent / cap 9 -> 2 batches');
  console.log(`worst-case 18 independent projects, cap 9: ${worstMs}ms`);

  const rand = mulberry32(42);
  const big = randomInput(rand, 18);
  big.capacity = 4;
  start = Date.now();
  const bigResult = planRelease(big);
  const bigMs = Date.now() - start;
  if (bigResult.ok) {
    check(validatePlan(big, bigResult) === null, '18-project random plan valid');
    console.log(`random 18 projects, cap 4: ${bigResult.batchCount} batches in ${bigMs}ms`);
  } else {
    console.log(`random 18 projects, cap 4: infeasible (${bigResult.witness.message}) in ${bigMs}ms`);
  }
}

unitTests();
console.log('unit tests done');
randomizedCrossCheck(2000);
perfTests();

if (failures > 0) {
  console.error(`\n${failures} check(s) FAILED`);
  process.exit(1);
}
console.log('\nall checks passed');
