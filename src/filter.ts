import type { NodeMetadata, NodeRecord } from './types.ts';

/** 支持的比较运算符（注意 != 与 >= 等双字符运算符要先于单字符匹配） */
const OPERATORS = ['!=', '>=', '<=', '=', '>', '<'] as const;
type Operator = (typeof OPERATORS)[number];

export interface Term {
  key: string;
  op: Operator;
  rawValue: string;
}

/**
 * 解析属性筛选表达式，例如：
 *   "zone=us-east"  "zone=us-east,weight>=50"  "role!=observer env=prod"
 * 多个条件以逗号或空白分隔，条件之间为 AND 关系。
 */
export function parseFilter(expression: string): Term[] {
  const terms: Term[] = [];
  for (const chunk of expression.split(/[\s,]+/).filter(Boolean)) {
    const op = OPERATORS.find((candidate) => chunk.includes(candidate));
    if (!op) throw new Error(`invalid filter term (missing operator): ${chunk}`);
    const idx = chunk.indexOf(op);
    const key = chunk.slice(0, idx).trim();
    const rawValue = chunk.slice(idx + op.length).trim();
    if (!key || !rawValue) throw new Error(`invalid filter term: ${chunk}`);
    terms.push({ key, op, rawValue });
  }
  if (terms.length === 0) throw new Error('empty filter expression');
  return terms;
}

/** 从元数据中按键取值：内建字段（zone/weight/role）优先，其次 labels，再次 attributes */
export function resolveKey(metadata: NodeMetadata, key: string): string | number | boolean | undefined {
  if (key === 'zone') return metadata.zone;
  if (key === 'weight') return metadata.weight;
  if (key === 'role') return metadata.role;
  if (key in metadata.labels) return metadata.labels[key];
  return metadata.attributes[key];
}

function compare(actual: string | number | boolean, op: Operator, raw: string): boolean {
  const numeric = typeof actual === 'number' || (typeof actual === 'string' && actual !== '' && !Number.isNaN(Number(actual)));
  if (numeric) {
    const a = Number(actual);
    const b = Number(raw);
    if (Number.isNaN(b)) return false;
    switch (op) {
      case '=': return a === b;
      case '!=': return a !== b;
      case '>': return a > b;
      case '>=': return a >= b;
      case '<': return a < b;
      case '<=': return a <= b;
    }
  }
  const a = String(actual);
  switch (op) {
    case '=': return a === raw;
    case '!=': return a !== raw;
    case '>': return a > raw;
    case '>=': return a >= raw;
    case '<': return a < raw;
    case '<=': return a <= raw;
  }
}

export function matches(metadata: NodeMetadata, terms: Term[]): boolean {
  return terms.every((term) => {
    const actual = resolveKey(metadata, term.key);
    if (actual === undefined) return term.op === '!=';
    return compare(actual, term.op, term.rawValue);
  });
}

/** 基于属性的动态拓扑子集筛选器 */
export class TopologyFilter {
  private readonly terms: Term[];

  constructor(expression: string) {
    this.terms = parseFilter(expression);
  }

  match(record: NodeRecord): boolean {
    return matches(record.metadata, this.terms);
  }

  select(records: NodeRecord[]): NodeRecord[] {
    return records.filter((record) => this.match(record));
  }
}
