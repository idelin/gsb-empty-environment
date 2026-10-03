/**
 * 向量化字符串过滤算子。
 *
 * 所有算子输入 StringColumn（紧凑字节数组 + 偏移数组），
 * 输出 Bitmap（压缩位图）。匹配过程在底层字节上批量进行，
 * 不为每行创建 JS 字符串对象；只有 LIKE 的复杂通配
 * 与正则路径才按需解码单行字符串。
 */

import { Bitmap } from "./bitmap.ts";
import { StringColumn } from "./column.ts";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/** 批量前缀匹配：在紧凑字节数组上逐行比对前置字节。 */
export function startsWith(column: StringColumn, prefix: string): Bitmap {
  const needle = encoder.encode(prefix);
  const n = needle.length;
  const { data, offsets } = column;
  const out = Bitmap.zeroes(column.rowCount);
  if (n === 0) return Bitmap.ones(column.rowCount);
  for (let row = 0; row < column.rowCount; row++) {
    const start = offsets[row];
    if (offsets[row + 1] - start < n) continue;
    let hit = true;
    for (let i = 0; i < n; i++) {
      if (data[start + i] !== needle[i]) {
        hit = false;
        break;
      }
    }
    if (hit) out.set(row);
  }
  return out;
}

/** 批量后缀匹配。 */
export function endsWith(column: StringColumn, suffix: string): Bitmap {
  const needle = encoder.encode(suffix);
  const n = needle.length;
  const { data, offsets } = column;
  const out = Bitmap.zeroes(column.rowCount);
  if (n === 0) return Bitmap.ones(column.rowCount);
  for (let row = 0; row < column.rowCount; row++) {
    const end = offsets[row + 1];
    if (end - offsets[row] < n) continue;
    let hit = true;
    for (let i = 0; i < n; i++) {
      if (data[end - n + i] !== needle[i]) {
        hit = false;
        break;
      }
    }
    if (hit) out.set(row);
  }
  return out;
}

/** 批量子串包含匹配（字节级朴素扫描，首字节跳检）。 */
export function contains(column: StringColumn, substr: string): Bitmap {
  const needle = encoder.encode(substr);
  const n = needle.length;
  const { data, offsets } = column;
  const out = Bitmap.zeroes(column.rowCount);
  if (n === 0) return Bitmap.ones(column.rowCount);
  const first = needle[0];
  for (let row = 0; row < column.rowCount; row++) {
    const start = offsets[row];
    const end = offsets[row + 1];
    if (end - start < n) continue;
    for (let p = start; p + n <= end; p++) {
      if (data[p] !== first) continue;
      let hit = true;
      for (let i = 1; i < n; i++) {
        if (data[p + i] !== needle[i]) {
          hit = false;
          break;
        }
      }
      if (hit) {
        out.set(row);
        break;
      }
    }
  }
  return out;
}

/** 批量等值匹配。 */
export function equals(column: StringColumn, literal: string): Bitmap {
  const needle = encoder.encode(literal);
  const n = needle.length;
  const { data, offsets } = column;
  const out = Bitmap.zeroes(column.rowCount);
  for (let row = 0; row < column.rowCount; row++) {
    const start = offsets[row];
    if (offsets[row + 1] - start !== n) continue;
    let hit = true;
    for (let i = 0; i < n; i++) {
      if (data[start + i] !== needle[i]) {
        hit = false;
        break;
      }
    }
    if (hit) out.set(row);
  }
  return out;
}

/** LIKE 模式编译结果：优先走字节级快路径，复杂通配退化为正则。 */
type LikePlan =
  | { kind: "prefix"; literal: string }
  | { kind: "suffix"; literal: string }
  | { kind: "contains"; literal: string }
  | { kind: "equals"; literal: string }
  | { kind: "regex"; regex: RegExp };

/**
 * 编译 SQL LIKE 模式（% 任意序列、_ 任意单字符，\ 转义）。
 * `url LIKE 'https://api.%'` 会编译为 prefix 快路径，
 * 全程字节级比对，不解码字符串。
 */
export function compileLike(pattern: string, escapeChar = "\\"): LikePlan {
  const literals: string[] = [];
  const wildcards: ("%" | "_")[] = [];
  let current = "";
  for (let i = 0; i < pattern.length; i++) {
    const ch = pattern[i];
    if (ch === escapeChar && i + 1 < pattern.length) {
      current += pattern[++i];
    } else if (ch === "%" || ch === "_") {
      literals.push(current);
      current = "";
      wildcards.push(ch);
    } else {
      current += ch;
    }
  }
  literals.push(current);

  if (wildcards.length === 0) return { kind: "equals", literal: literals[0] };
  if (wildcards.every((w) => w === "%")) {
    if (wildcards.length === 1) {
      const [head, tail] = literals;
      if (tail === "") return { kind: "prefix", literal: head };
      if (head === "") return { kind: "suffix", literal: tail };
      // "a%b" 型：前后都有字面量，无法用单一字节扫描表达，走正则。
    } else if (wildcards.length === 2 && literals[0] === "" && literals[2] === "") {
      return { kind: "contains", literal: literals[1] };
    }
  }
  return { kind: "regex", regex: likeToRegExp(literals, wildcards) };
}

function likeToRegExp(literals: string[], wildcards: ("%" | "_")[]): RegExp {
  let source = "^";
  for (let i = 0; i < wildcards.length; i++) {
    source += escapeRegExp(literals[i]);
    source += wildcards[i] === "%" ? "[\\s\\S]*" : "[\\s\\S]";
  }
  source += escapeRegExp(literals[literals.length - 1]) + "$";
  return new RegExp(source);
}

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** 批量 LIKE 过滤：编译一次模式，向量化扫描整列。 */
export function like(column: StringColumn, pattern: string, escapeChar = "\\"): Bitmap {
  const plan = compileLike(pattern, escapeChar);
  switch (plan.kind) {
    case "prefix":
      return startsWith(column, plan.literal);
    case "suffix":
      return endsWith(column, plan.literal);
    case "contains":
      return contains(column, plan.literal);
    case "equals":
      return equals(column, plan.literal);
    case "regex":
      return matchesRegex(column, plan.regex);
  }
}

/**
 * 批量正则匹配。正则需要字符语义，逐行用共享 TextDecoder
 * 解码到临时字符串后立即判定，不保留任何行对象。
 * 传入的 /g 标志会被移除，避免 lastIndex 状态污染批量结果。
 */
export function matchesRegex(column: StringColumn, regex: RegExp): Bitmap {
  const flags = regex.flags.replace("g", "");
  const tester = new RegExp(regex.source, flags);
  const out = Bitmap.zeroes(column.rowCount);
  for (let row = 0; row < column.rowCount; row++) {
    if (tester.test(decoder.decode(column.rowBytes(row)))) out.set(row);
  }
  return out;
}
