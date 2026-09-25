/**
 * 子集最小不可满足核（subset-minimal unsatisfiable core）。
 * 基于删除的最小化：逐条尝试移除义务，若移除后仍不可满足则永久移除。
 * 结果保证：核本身不可满足，且核的任意真子集都可满足。
 */
import type { Obligation } from './obligations.ts';

export function minimalUnsatCore(
  obligations: Obligation[],
  isSatisfiable: (obs: Obligation[]) => boolean,
): Obligation[] {
  if (isSatisfiable(obligations)) {
    throw new Error('minimalUnsatCore 要求输入集合本身不可满足');
  }
  let core = [...obligations];
  let changed = true;
  while (changed) {
    changed = false;
    for (const ob of [...core]) {
      const candidate = core.filter((o) => o !== ob);
      if (candidate.length > 0 && !isSatisfiable(candidate)) {
        core = candidate;
        changed = true;
      }
    }
  }
  return core;
}
