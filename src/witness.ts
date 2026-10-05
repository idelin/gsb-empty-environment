import { BudgetExceeded, Normalized, SearchEnv, combinations, search } from './internal';
import { Witness } from './types';

function popcount(x: number): number {
  let c = 0;
  while (x) {
    x &= x - 1;
    c++;
  }
  return c;
}

/**
 * 容量见证：枚举 所有作品子集 S（|作品| <= 12，最多 4095 个），
 * 设 N(S) 为 S 中作品的合格评委并集，n = |N(S)|。检查两个由
 * “评委对联合限制”导出的必要条件：
 *
 *  A. 评委席位上界：评委 j 每评一份作品消耗 K-1 个评委对名额，每个对至多 P 次，
 *     故 load(j) <= floor(P*(n-1)/(K-1))，总席位 <= Σ min(可评作品数, 该上界)。
 *  B. 评委对总数上界：每份作品需要 C(K,2) 个评委对，
 *     总需求 <= Σ_{评委对} min(P, 共同可评的 S 中作品数)。
 *
 * 任一被违反即返回见证（按子集大小升序，保证找到的是极小见证）。
 */
export function findCapacityWitness(norm: Normalized): Witness | null {
  const { candidates, K, P, workIds, judgeIds } = norm;
  const W = workIds.length;
  const J = judgeIds.length;

  const judgeWorkMask = new Array<number>(J).fill(0);
  for (let w = 0; w < W; w++) {
    for (const j of candidates[w]) judgeWorkMask[j] |= 1 << w;
  }

  const indices = Array.from({ length: W }, (_, i) => i);
  for (let size = 1; size <= W; size++) {
    for (const subset of combinations(indices, size)) {
      let mask = 0;
      for (const w of subset) mask |= 1 << w;
      const inN: number[] = [];
      for (let j = 0; j < J; j++) {
        if ((judgeWorkMask[j] & mask) !== 0) inN.push(j);
      }
      const n = inN.length;
      const sWorkIds = subset.map((i) => workIds[i]);
      const sJudgeIds = inN.map((j) => judgeIds[j]);

      // 条件 A：评委席位上界
      const loadCap = K === 1 ? size : Math.floor((P * (n - 1)) / (K - 1));
      let maxSlots = 0;
      for (const j of inN) maxSlots += Math.min(popcount(judgeWorkMask[j] & mask), loadCap);
      const required = K * size;
      if (required > maxSlots) {
        return {
          type: 'capacity',
          bound: 'judge-pair-slots',
          workIds: sWorkIds,
          judgeIds: sJudgeIds,
          requiredSlots: required,
          maxSlots,
          detail:
            `作品子集 {${sWorkIds.join(', ')}}（${size} 份）只有 ${n} 名合格评委；` +
            `在评委对联合限制（任意评委对最多共评 ${P} 份）下，每名评委最多承担 ${loadCap} 份，` +
            `合计最多 ${maxSlots} 个评审席位，小于所需 ${required} 个`,
        };
      }

      // 条件 B：评委对总数上界
      let pairCap = 0;
      for (let a = 0; a < inN.length; a++) {
        for (let b = a + 1; b < inN.length; b++) {
          pairCap += Math.min(P, popcount(judgeWorkMask[inN[a]] & judgeWorkMask[inN[b]] & mask));
        }
      }
      const needPairs = (K * (K - 1)) / 2 * size;
      if (needPairs > pairCap) {
        return {
          type: 'capacity',
          bound: 'pair-cojudging',
          workIds: sWorkIds,
          judgeIds: sJudgeIds,
          requiredSlots: needPairs,
          maxSlots: pairCap,
          detail:
            `作品子集 {${sWorkIds.join(', ')}}（${size} 份）需要 ${needPairs} 次评委对共评，` +
            `但其合格评委集合内的评委对容量（每对最多共评 ${P} 份）合计仅 ${pairCap} 次`,
        };
      }
    }
  }
  return null;
}

/**
 * 组合核心：当容量必要条件全部满足但完备搜索仍判定不可行时，
 * 通过逐删除法求一个极小不可行作品子集（删除其中任意一份后可行）。
 * 该核心本身即为“评委对联合限制导致无解”的严格见证。
 */
export function findCombinatorialCore(
  norm: Normalized,
  allCombos: number[][][],
  onNodes: (n: number) => void,
): number[] {
  const { K, P, nodeLimit, judgeIds } = norm;
  const J = judgeIds.length;
  let core = allCombos.map((_, i) => i);
  for (const w of core.slice()) {
    const trial = core.filter((x) => x !== w);
    if (trial.length === 0) continue;
    const env: SearchEnv = { J, K, P, nodeLimit, nodes: { count: 0 } };
    let sol: number[][] | null;
    try {
      sol = search(trial.map((i) => allCombos[i]), env, 0, trial.length);
    } finally {
      onNodes(env.nodes.count);
    }
    if (sol === null) core = trial;
  }
  return core;
}
