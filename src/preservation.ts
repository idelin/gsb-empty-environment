// 依赖保持检查：令 G = ∪_i π_Ri(F)，判断 G ⊨ F。
// 不显式物化 G（指数级），而是对每个 FD 用标准的“投影闭包”算法：
//   Z := X
//   重复：对每个分量 Ri，Z := Z ∪ ((Z ∩ Ri)+_F ∩ Ri)，直至不动点。
// 最终 Z = X+_G。若 rhs ⊆ Z 则该 FD 被保持，否则给出闭包缺口。
// 迭代顺序固定（分量按输入顺序），结果确定且可重放。
import type { FD, FdPreservation, Spec } from "./types.ts";

/** 在 FD 集 fds 下计算属性集 start 的闭包（含 start），结果排序。 */
export function closure(start: string[], fds: FD[]): string[] {
  const result = new Set(start);
  let changed = true;
  while (changed) {
    changed = false;
    for (const fd of fds) {
      if (fd.lhs.every((a) => result.has(a))) {
        for (const b of fd.rhs) {
          if (!result.has(b)) {
            result.add(b);
            changed = true;
          }
        }
      }
    }
  }
  return [...result].sort();
}

/** 计算 X 在 G = ∪ π_Ri(F) 下的闭包，不物化 G。 */
export function projectedClosure(start: string[], spec: Spec): string[] {
  const z = new Set(start);
  let changed = true;
  while (changed) {
    changed = false;
    for (const comp of spec.decomposition) {
      const inside = [...z].filter((a) => comp.includes(a));
      const inner = new Set(closure(inside, spec.fds));
      for (const a of comp) {
        if (inner.has(a) && !z.has(a)) {
          z.add(a);
          changed = true;
        }
      }
    }
  }
  return [...z].sort();
}

/** 逐个检查 F 中的 FD 是否被分解保持。 */
export function checkPreservation(spec: Spec): FdPreservation[] {
  return spec.fds.map((fd, fdIndex) => {
    const reachable = projectedClosure(fd.lhs, spec);
    const full = closure(fd.lhs, spec.fds);
    const reachableSet = new Set(reachable);
    const closureGap = full.filter((a) => !reachableSet.has(a));
    const missingRhs = fd.rhs.filter((a) => !reachableSet.has(a));
    return {
      fdIndex,
      fd,
      preserved: missingRhs.length === 0,
      reachableClosure: reachable,
      fullClosure: full,
      closureGap,
      missingRhs,
    };
  });
}
