/**
 * 路径枚举核对：用有限字母表枚举短路径，交叉验证
 *   1. 编译期报告的每个冲突见证在运行期确实同优先级并列；
 *   2. 枚举范围内不存在未报告的同优先级并列。
 * 合一检测在理论上已精确，枚举核对作为独立实现用于互相印证（测试与调试）。
 */

import type { CompiledRouter } from "./router.ts";

/** 枚举 alphabet 上长度 0..maxLength 的全部段序列。 */
export function enumeratePaths(alphabet: readonly string[], maxLength: number): string[][] {
  const out: string[][] = [[]];
  let frontier: string[][] = [[]];
  for (let len = 1; len <= maxLength; len++) {
    const next: string[][] = [];
    for (const prefix of frontier) {
      for (const token of alphabet) {
        const path = [...prefix, token];
        out.push(path);
        next.push(path);
      }
    }
    frontier = next;
  }
  return out;
}

export interface CrossCheckOptions {
  /** 枚举的最大段数，默认 4。 */
  maxLength?: number;
  /** 除字面外的额外占位段（代表参数取值），默认 ["p0", "p1"]。 */
  extraTokens?: string[];
}

interface TiedGroup {
  indices: number[];
  path: string;
}

function topTied<T>(router: CompiledRouter<T>, path: string): TiedGroup | null {
  const matches = router.matchAll(path);
  if (matches.length < 2) return null;
  const top = matches[0].score;
  const indices = matches
    .filter(
      (m) => m.score.literals === top.literals && m.score.omissions === top.omissions,
    )
    .map((m) => m.index);
  return indices.length >= 2 ? { indices, path } : null;
}

/** 返回问题列表；空数组表示核对通过。 */
export function crossCheckConflicts<T>(
  router: CompiledRouter<T>,
  options: CrossCheckOptions = {},
): string[] {
  const problems: string[] = [];
  const maxLength = options.maxLength ?? 4;
  const extraTokens = options.extraTokens ?? ["p0", "p1"];

  const literals = new Set<string>();
  for (const route of router.routes) {
    for (const exp of route.expansions) {
      for (const seg of exp.segments) {
        if (seg.kind === "literal") literals.add(seg.value);
      }
    }
  }
  const alphabet = [...literals, ...extraTokens];

  const reported = new Set<string>();
  for (const c of router.conflicts) {
    reported.add(`${c.firstIndex}:${c.secondIndex}`);
  }

  // 方向一：运行期并列 => 必须已报告
  for (const segments of enumeratePaths(alphabet, maxLength)) {
    const path = "/" + segments.join("/");
    const tied = topTied(router, path);
    if (tied === null) continue;
    for (let x = 0; x < tied.indices.length; x++) {
      for (let y = x + 1; y < tied.indices.length; y++) {
        const lo = Math.min(tied.indices[x], tied.indices[y]);
        const hi = Math.max(tied.indices[x], tied.indices[y]);
        if (!reported.has(`${lo}:${hi}`)) {
          problems.push(
            `unreported same-priority overlap between #${lo} and #${hi} at ${JSON.stringify(path)}`,
          );
        }
      }
    }
  }

  // 方向二：已报告的见证 => 运行期必须确实并列
  for (const c of router.conflicts) {
    const tied = topTied(router, c.witness);
    if (
      tied === null ||
      !tied.indices.includes(c.firstIndex) ||
      !tied.indices.includes(c.secondIndex)
    ) {
      problems.push(
        `witness ${JSON.stringify(c.witness)} for #${c.firstIndex}/#${c.secondIndex} ` +
          `does not reproduce a same-priority tie at runtime`,
      );
    }
  }

  return problems;
}
