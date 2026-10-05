// 有限表格追逐（tableau chase）判定无损连接。
//
// 确定性 / 可重放约定：
//  1. 符号命名规范：第 k 列（属性按字典序编号）的杰出变量为 a<k>；
//     第 i 行第 k 列的非杰出变量为 b<i>_<k>。名称即规范形式。
//  2. 合并规则（合流所必需）：杰出变量优先；两个非杰出变量合并时
//     保留名称字典序较小者。因此任意合并序列都收敛到同一结果。
//  3. 扫描顺序固定：FD 按输入顺序、行对按 (i<j) 字典序、右部属性按
//     字典序，每次只执行找到的第一个合并，然后从头重新扫描。
//     相同的输入永远产生完全相同的证据序列。
import type { ChaseResult, FD, MergeStep, Spec } from "./types.ts";

function isDistinguished(name: string): boolean {
  return name.startsWith("a");
}

/** 合并裁决：返回应保留的符号名。 */
export function mergeWinner(x: string, y: string): string {
  if (x === y) return x;
  const dx = isDistinguished(x);
  const dy = isDistinguished(y);
  if (dx !== dy) return dx ? x : y;
  return x < y ? x : y;
}

/** 构造初始 tableau：行 i 对应分解分量 Ri。 */
export function initialTableau(spec: Spec): string[][] {
  const colOf = new Map(spec.attributes.map((a, k) => [a, k]));
  return spec.decomposition.map((comp, i) => {
    const inComp = new Set(comp);
    return spec.attributes.map((a, k) =>
      inComp.has(a) ? `a${k}` : `b${i}_${k}`
    );
  });
}

interface FoundMerge {
  fdIndex: number;
  fd: FD;
  rowA: number;
  rowB: number;
  attribute: string;
  eliminated: string;
  kept: string;
}

/** 按固定顺序扫描，找到第一个可执行的合并；没有则返回 null。 */
function findNextMerge(
  spec: Spec,
  tableau: string[][],
  colOf: Map<string, number>
): FoundMerge | null {
  for (let fdIndex = 0; fdIndex < spec.fds.length; fdIndex++) {
    const fd = spec.fds[fdIndex];
    const lhsCols = fd.lhs.map((a) => colOf.get(a)!);
    for (let i = 0; i < tableau.length; i++) {
      for (let j = i + 1; j < tableau.length; j++) {
        const agree = lhsCols.every((c) => tableau[i][c] === tableau[j][c]);
        if (!agree) continue;
        for (const attr of fd.rhs) {
          const c = colOf.get(attr)!;
          const x = tableau[i][c];
          const y = tableau[j][c];
          if (x === y) continue;
          const kept = mergeWinner(x, y);
          return {
            fdIndex,
            fd,
            rowA: i,
            rowB: j,
            attribute: attr,
            eliminated: x === kept ? y : x,
            kept,
          };
        }
      }
    }
  }
  return null;
}

/** 执行 chase，返回最终 tableau 与完整合并证据。 */
export function chase(spec: Spec): ChaseResult {
  const colOf = new Map(spec.attributes.map((a, k) => [a, k]));
  const tableau = initialTableau(spec);
  const merges: MergeStep[] = [];

  // 有限性：每步把某个符号名全局替换为另一个，不同符号总数严格减少，
  // 上界为 行数 × 列数，因此 chase 必然终止。
  for (;;) {
    const found = findNextMerge(spec, tableau, colOf);
    if (!found) break;
    for (const row of tableau) {
      for (let c = 0; c < row.length; c++) {
        if (row[c] === found.eliminated) row[c] = found.kept;
      }
    }
    merges.push({ step: merges.length + 1, ...found });
  }

  const witnessRow = tableau.findIndex((row) => row.every(isDistinguished));
  return {
    tableau,
    merges,
    lossless: witnessRow !== -1,
    witnessRow,
  };
}
