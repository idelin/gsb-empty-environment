/**
 * 输入模型：布尔特性 + 互斥(mutex) / 至多其一(atMostOne) / 蕴含(implies) 约束。
 * 所有约束统一编译为二元子句（2-CNF），文字编码：正文字 = 2*f，负文字 = 2*f+1。
 */

export interface ModelSpec {
  features: string[];
  mutex?: [string, string][];
  atMostOne?: string[][];
  implies?: [string, string][];
}

export interface Clause {
  a: number; // 文字 a
  b: number; // 文字 b，子句含义为 (a ∨ b)
  desc: string; // 人类可读的来源描述（用于解释）
}

export interface CompiledModel {
  n: number;
  features: string[];
  clauses: Clause[];
  constrained: boolean[]; // 每个特性是否出现在至少一条约束中
}

export const posLit = (f: number): number => 2 * f;
export const negLit = (f: number): number => 2 * f + 1;
export const notLit = (l: number): number => l ^ 1;
export const litFeature = (l: number): number => l >> 1;
export const litValue = (l: number): number => (l & 1) === 0 ? 1 : 0;

export function litName(l: number, features: string[]): string {
  return `${features[litFeature(l)]}=${litValue(l)}`;
}

export function compileModel(spec: ModelSpec): CompiledModel {
  if (!spec || !Array.isArray(spec.features)) {
    throw new Error('输入缺少 "features" 数组');
  }
  const features = spec.features.slice();
  const n = features.length;
  if (n < 2) throw new Error(`特性数量至少为 2，当前为 ${n}`);
  if (n > 12) throw new Error(`特性数量最多为 12，当前为 ${n}`);
  const seen = new Set<string>();
  for (const f of features) {
    if (typeof f !== 'string' || f.length === 0) throw new Error('特性名必须为非空字符串');
    if (seen.has(f)) throw new Error(`特性名重复: ${f}`);
    seen.add(f);
  }
  const index = new Map<string, number>();
  features.forEach((f, i) => index.set(f, i));
  const lookup = (name: string): number => {
    const i = index.get(name);
    if (i === undefined) throw new Error(`约束引用了未知特性: ${name}`);
    return i;
  };

  const clauses: Clause[] = [];
  const constrained = new Array<boolean>(n).fill(false);
  const mark = (...fs: number[]) => { for (const f of fs) constrained[f] = true; };

  for (const pair of spec.mutex ?? []) {
    const [x, y] = pair;
    const i = lookup(x), j = lookup(y);
    if (i === j) throw new Error(`互斥约束两端相同: ${x}`);
    clauses.push({ a: negLit(i), b: negLit(j), desc: `互斥(${x}, ${y})` });
    mark(i, j);
  }
  for (const group of spec.atMostOne ?? []) {
    if (!Array.isArray(group) || group.length < 2) {
      throw new Error('atMostOne 组至少需要 2 个特性');
    }
    const ids = group.map(lookup);
    if (new Set(ids).size !== ids.length) throw new Error(`atMostOne 组内特性重复: ${group}`);
    for (let p = 0; p < ids.length; p++) {
      for (let q = p + 1; q < ids.length; q++) {
        clauses.push({
          a: negLit(ids[p]),
          b: negLit(ids[q]),
          desc: `至多其一(${group.join(', ')})`,
        });
      }
    }
    mark(...ids);
  }
  for (const pair of spec.implies ?? []) {
    const [x, y] = pair;
    const i = lookup(x), j = lookup(y);
    if (i === j) throw new Error(`蕴含约束两端相同: ${x}`);
    clauses.push({ a: negLit(i), b: posLit(j), desc: `蕴含 ${x} ⇒ ${y}` });
    mark(i, j);
  }

  return { n, features, clauses, constrained };
}
