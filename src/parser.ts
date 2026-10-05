// 输入解析与校验。所有集合在解析后即排序去重，保证后续流程确定性。
import type { FD, Spec } from "./types.ts";

export const MAX_ATTRIBUTES = 10;

export class ParseError extends Error {}

const NAME_RE = /^[A-Za-z][A-Za-z0-9_]*$/;

/**
 * 解析属性列表。支持两种写法：
 *  - 分隔形式："A,B,C" 或 "A B C"（逗号/空白分隔，属性名可多字符）
 *  - 紧凑形式："ABC"（每个字符一个属性，仅当不含分隔符时）
 */
export function parseAttrList(text: string): string[] {
  const trimmed = text.trim();
  if (trimmed === "") throw new ParseError("属性列表为空");
  let parts: string[];
  if (/[,\s]/.test(trimmed)) {
    parts = trimmed.split(/[,\s]+/).filter((p) => p.length > 0);
  } else if (NAME_RE.test(trimmed) && trimmed.length > 1 && /^[A-Za-z]+$/.test(trimmed)) {
    // 紧凑形式：仅纯字母且长度 > 1 时按单字符拆分
    parts = trimmed.split("");
  } else {
    parts = [trimmed];
  }
  for (const p of parts) {
    if (!NAME_RE.test(p)) throw new ParseError(`非法属性名: "${p}"`);
  }
  const unique = [...new Set(parts)];
  return unique.sort();
}

/** 解析单个 FD，形如 "A->BC" 或 "AB -> C,D"。 */
export function parseFd(text: string): FD {
  const m = text.split("->");
  if (m.length !== 2) throw new ParseError(`非法函数依赖（缺少 ->）: "${text}"`);
  const lhs = parseAttrList(m[0]);
  const rhs = parseAttrList(m[1]);
  return { lhs, rhs };
}

/** 解析 FD 集：以 ";" 或换行分隔。保持输入顺序。 */
export function parseFds(text: string): FD[] {
  const chunks = text.split(/[;\n]+/).map((s) => s.trim()).filter((s) => s.length > 0);
  if (chunks.length === 0) throw new ParseError("函数依赖集为空");
  return chunks.map(parseFd);
}

/** 解析分解：分量之间以 "|" 或空白分隔，如 "ABC CDE" 或 "ABC|CDE"。 */
export function parseDecomposition(text: string): string[][] {
  const chunks = text.split(/[|\s]+/).map((s) => s.trim()).filter((s) => s.length > 0);
  if (chunks.length === 0) throw new ParseError("分解为空");
  return chunks.map(parseAttrList);
}

/** 组装并校验完整规格。 */
export function buildSpec(attrsText: string, fdsText: string, decompText: string): Spec {
  const attributes = parseAttrList(attrsText);
  if (attributes.length > MAX_ATTRIBUTES) {
    throw new ParseError(`属性数 ${attributes.length} 超过上限 ${MAX_ATTRIBUTES}`);
  }
  const universe = new Set(attributes);
  const fds = parseFds(fdsText);
  const decomposition = parseDecomposition(decompText);

  const checkKnown = (attrs: string[], where: string) => {
    for (const a of attrs) {
      if (!universe.has(a)) throw new ParseError(`${where} 引用了未知属性 "${a}"`);
    }
  };
  fds.forEach((fd, i) => {
    checkKnown(fd.lhs, `FD#${i} 左部`);
    checkKnown(fd.rhs, `FD#${i} 右部`);
  });
  decomposition.forEach((comp, i) => checkKnown(comp, `分解分量 R${i}`));

  const covered = new Set(decomposition.flat());
  for (const a of attributes) {
    if (!covered.has(a)) throw new ParseError(`分解未覆盖属性 "${a}"`);
  }
  return { attributes, fds, decomposition };
}
