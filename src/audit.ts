/**
 * 审计器核心：
 *  1. 枚举有限输入域中的全部赋值，计算每个赋值的缓存键与构建输出；
 *  2. 单变量敏感性分析：哪些输入被键「声明」（翻转会改键）、哪些被构建「使用」（翻转会改输出）；
 *  3. 按差异变量数（汉明距离）递增地搜索「键相同但输出不同」的输入对，
 *     用包含最小性（inclusion-minimal）剪枝，只保留最小反例；
 *  4. 每个反例分类为「影响产物」或「仅影响日志」。
 */

import type { Assignment, BuildOutput, BuildSpec } from "./model.ts";
import { encodeAssignment, enumerateAssignments } from "./model.ts";

/** 差异类型：artifact = 产物不同；log-only = 产物相同、仅日志不同 */
export type DiffKind = "artifact" | "log-only";

export interface Counterexample {
  /** 两个赋值之间取值不同的变量名（排序后），即差异变量集 */
  readonly vars: readonly string[];
  readonly kind: DiffKind;
  /** 两个赋值共享的缓存键 */
  readonly key: string;
  readonly a: Assignment;
  readonly b: Assignment;
  readonly outputA: BuildOutput;
  readonly outputB: BuildOutput;
}

export interface AuditOptions {
  /** 搜索的最大差异变量数，默认搜索全部距离 */
  readonly maxDistance?: number;
}

export interface AuditResult {
  readonly buildName: string;
  readonly totalAssignments: number;
  readonly distinctKeys: number;
  /** 键实际依赖的输入（存在某上下文，翻转它会改变键） */
  readonly declared: readonly string[];
  /** 输出实际依赖的输入（存在某上下文，翻转它会改变产物或日志） */
  readonly used: readonly string[];
  /** 使用了却没声明：缓存不命中的根因 */
  readonly undeclaredButUsed: readonly string[];
  /** 声明了却没使用：键里的冗余输入 */
  readonly declaredButUnused: readonly string[];
  /** 包含最小反例，按差异变量数升序 */
  readonly counterexamples: readonly Counterexample[];
}

function logsEqual(x: readonly string[], y: readonly string[]): boolean {
  return x.length === y.length && x.every((line, i) => line === y[i]);
}

function outputsEqual(x: BuildOutput, y: BuildOutput): boolean {
  return x.artifact === y.artifact && logsEqual(x.logs, y.logs);
}

function classify(x: BuildOutput, y: BuildOutput): DiffKind | null {
  if (x.artifact !== y.artifact) return "artifact";
  if (!logsEqual(x.logs, y.logs)) return "log-only";
  return null;
}

function* combinations<T>(items: readonly T[], size: number): Generator<T[]> {
  if (size === 0) {
    yield [];
    return;
  }
  for (let i = 0; i <= items.length - size; i++) {
    for (const rest of combinations(items.slice(i + 1), size - 1)) {
      yield [items[i], ...rest];
    }
  }
}

function* cartesian(lists: readonly (readonly string[])[]): Generator<string[]> {
  if (lists.length === 0) {
    yield [];
    return;
  }
  const [head, ...tail] = lists;
  for (const value of head) {
    for (const rest of cartesian(tail)) {
      yield [value, ...rest];
    }
  }
}

function isSubsetOf(small: readonly string[], big: readonly string[]): boolean {
  return small.every((v) => big.includes(v));
}

function isStrictSubsetOf(small: readonly string[], big: readonly string[]): boolean {
  return small.length < big.length && isSubsetOf(small, big);
}

function sameSet(x: readonly string[], y: readonly string[]): boolean {
  return x.length === y.length && isSubsetOf(x, y);
}

export function audit(spec: BuildSpec, options: AuditOptions = {}): AuditResult {
  const inputs = spec.inputs;
  const assignments = enumerateAssignments(inputs);
  const keys = assignments.map((a) => spec.key(a));
  const outputs = assignments.map((a) => spec.run(a));
  const indexOf = new Map<string, number>();
  assignments.forEach((a, i) => indexOf.set(encodeAssignment(a, inputs), i));

  // --- 单变量敏感性：声明性（键依赖）与使用性（输出依赖） ---
  const declared = new Set<string>();
  const used = new Set<string>();
  for (let i = 0; i < assignments.length; i++) {
    for (const input of inputs) {
      for (const value of input.domain) {
        if (value === assignments[i][input.name]) continue;
        if (declared.has(input.name) && used.has(input.name)) break;
        const neighbor = { ...assignments[i], [input.name]: value };
        const j = indexOf.get(encodeAssignment(neighbor, inputs));
        if (j === undefined) continue;
        if (keys[j] !== keys[i]) declared.add(input.name);
        if (!outputsEqual(outputs[j], outputs[i])) used.add(input.name);
      }
    }
  }

  // --- 按汉明距离递增搜索最小反例 ---
  const maxDistance = Math.min(options.maxDistance ?? inputs.length, inputs.length);
  const counterexamples: Counterexample[] = [];
  const minimalSets: string[][] = [];
  const varIndices = inputs.map((_, idx) => idx);

  for (let distance = 1; distance <= maxDistance; distance++) {
    for (const subset of combinations(varIndices, distance)) {
      const subsetNames = subset.map((vi) => inputs[vi].name).sort();
      // 已包含某个最小反例集合的超集不可能是最小反例，整枝剪掉
      if (minimalSets.some((s) => isSubsetOf(s, subsetNames))) continue;

      for (let i = 0; i < assignments.length; i++) {
        const base = assignments[i];
        const alternatives = subset.map((vi) =>
          inputs[vi].domain.filter((v) => v !== base[inputs[vi].name]),
        );
        for (const combo of cartesian(alternatives)) {
          const b = { ...base };
          subset.forEach((vi, k) => {
            b[inputs[vi].name] = combo[k];
          });
          const j = indexOf.get(encodeAssignment(b, inputs));
          if (j === undefined || j <= i) continue; // 每对只检查一次
          if (keys[j] !== keys[i]) continue;
          const kind = classify(outputs[i], outputs[j]);
          if (kind === null) continue;
          if (counterexamples.some((c) => sameSet(c.vars, subsetNames) && c.kind === kind)) {
            continue; // 同差异集、同类型已记录，保留首个见证
          }
          minimalSets.push(subsetNames);
          counterexamples.push({
            vars: subsetNames,
            kind,
            key: keys[i],
            a: base,
            b,
            outputA: outputs[i],
            outputB: outputs[j],
          });
        }
      }
    }
  }

  const declaredList = inputs.map((v) => v.name).filter((n) => declared.has(n));
  const usedList = inputs.map((v) => v.name).filter((n) => used.has(n));
  return {
    buildName: spec.name,
    totalAssignments: assignments.length,
    distinctKeys: new Set(keys).size,
    declared: declaredList,
    used: usedList,
    undeclaredButUsed: usedList.filter((n) => !declared.has(n)),
    declaredButUnused: declaredList.filter((n) => !used.has(n)),
    counterexamples,
  };
}
