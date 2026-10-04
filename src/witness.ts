import type { AvoidanceWitness, CapacityWitness } from './types.ts';

/**
 * 回避见证：找出可用评委少于 teamSize 的作品中缺口最大的一个。
 */
export function findAvoidanceWitness(
  workIds: readonly string[],
  eligible: readonly (readonly number[])[],
  judgeIds: readonly string[],
  teamSize: number,
): AvoidanceWitness | null {
  let worst: AvoidanceWitness | null = null;
  for (let w = 0; w < workIds.length; w++) {
    const list = eligible[w]!;
    if (list.length < teamSize && (worst === null || list.length < worst.eligibleJudgeIds.length)) {
      worst = {
        kind: 'avoidance',
        workId: workIds[w]!,
        eligibleJudgeIds: list.map((j) => judgeIds[j]!),
        required: teamSize,
      };
    }
  }
  return worst;
}

function popcountBigint(value: bigint): number {
  let count = 0;
  let v = value;
  while (v !== 0n) {
    count += Number(v & 1n);
    v >>= 1n;
  }
  return count;
}

function popcount32(value: number): number {
  let count = 0;
  let v = value;
  while (v !== 0) {
    count += v & 1;
    v >>>= 1;
  }
  return count;
}

/**
 * 容量见证：枚举全部 2^m 个作品子集 S（m ≤ 12），
 * 设 T 为 S 的可用评委池。S 需要 pairDemandPerWork * |S| 个评委对名额，
 * T 内评委对在联合限制下至多提供 maxPairShared * C(|T|,2) 个。
 * 返回缺口最大（并列时子集最小）的见证。
 */
export function findCapacityWitness(
  workIds: readonly string[],
  eligibleMasks: readonly bigint[],
  judgeIds: readonly string[],
  teamSize: number,
  maxPairShared: number,
): CapacityWitness | null {
  const m = workIds.length;
  const pairDemandPerWork = (teamSize * (teamSize - 1)) / 2;
  let bestDeficiency = 0;
  let bestSize = 0;
  let bestMask = 0;
  let bestPool = 0n;

  for (let mask = 1; mask < 1 << m; mask++) {
    let pool = 0n;
    for (let w = 0; w < m; w++) {
      if ((mask & (1 << w)) !== 0) pool |= eligibleMasks[w]!;
    }
    const size = popcount32(mask);
    const poolSize = popcountBigint(pool);
    const demand = pairDemandPerWork * size;
    const capacity = (maxPairShared * poolSize * (poolSize - 1)) / 2;
    const deficiency = demand - capacity;
    if (
      deficiency > 0 &&
      (deficiency > bestDeficiency || (deficiency === bestDeficiency && (bestMask === 0 || size < bestSize)))
    ) {
      bestDeficiency = deficiency;
      bestSize = size;
      bestMask = mask;
      bestPool = pool;
    }
  }

  if (bestMask === 0) return null;

  const witnessWorks: string[] = [];
  for (let w = 0; w < m; w++) {
    if ((bestMask & (1 << w)) !== 0) witnessWorks.push(workIds[w]!);
  }
  const witnessJudges: string[] = [];
  for (let j = 0; j < judgeIds.length; j++) {
    if ((bestPool & (1n << BigInt(j))) !== 0n) witnessJudges.push(judgeIds[j]!);
  }
  return {
    kind: 'capacity',
    workIds: witnessWorks,
    judgeIds: witnessJudges,
    demand: pairDemandPerWork * bestSize,
    capacity: (maxPairShared * witnessJudges.length * (witnessJudges.length - 1)) / 2,
  };
}
