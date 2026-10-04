// 小表穷举等价性检查：
// 在给定值域（含 NULL）上枚举所有行组合、所有不超过 maxRows 行的表，
// 对每一对表 (L, R) 比较两个计划的输出多重集是否一致。
import type { Row, Value } from './expr.js';
import type { Plan } from './plan.js';
import { execute } from './plan.js';

export interface EquivResult {
  equivalent: boolean;
  checkedCases: number;
  counterexample?: {
    left: Row[];
    right: Row[];
    before: Row[];
    after: Row[];
  };
}

// 多重集比较：行序列化为可排序的键（NULL 有专门表示）。
export function rowSortKey(r: Row): string {
  return Object.keys(r)
    .sort()
    .map((k) => {
      const v: Value = r[k];
      const text = v === null ? 'NULL' : `${typeof v}:${String(v)}`;
      return `${k}=${text}`;
    })
    .join(',');
}

export function sameMultiset(a: Row[], b: Row[]): boolean {
  if (a.length !== b.length) return false;
  const ka = a.map(rowSortKey).sort();
  const kb = b.map(rowSortKey).sort();
  return ka.every((v, i) => v === kb[i]);
}

// 枚举 cols 列、每列取自 domain、长度 0..maxRows 的所有表（行的多重集无意义，
// 这里按有序行列表枚举，数量 = sum_{n<=maxRows} |domain|^（cols*n)）。
export function* enumerateTables(cols: string[], domain: Value[], maxRows: number): Generator<Row[]> {
  const rowCount = Math.pow(domain.length, cols.length);
  const allRows: Row[] = [];
  const rec = (ci: number, acc: Row): void => {
    if (ci === cols.length) {
      allRows.push({ ...acc });
      return;
    }
    for (const v of domain) {
      acc[cols[ci]] = v;
      rec(ci + 1, acc);
    }
  };
  rec(0, {});

  yield [];
  const pick = (n: number, start: number, acc: Row[]): Generator<Row[]> => {
    if (n === 0) {
      return (function* () {
        yield acc.slice();
      })();
    }
    return (function* () {
      for (let i = start; i < rowCount; i++) {
        acc.push(allRows[i]);
        yield* pick(n - 1, i, acc); // 允许重复行（包语义）
        acc.pop();
      }
    })();
  };
  for (let n = 1; n <= maxRows; n++) {
    yield* pick(n, 0, []);
  }
}

export interface CheckOptions {
  leftCols: string[];
  rightCols: string[];
  domain: Value[];
  maxRows: number;
  makeBefore: (left: Row[], right: Row[]) => Plan;
  makeAfter: (left: Row[], right: Row[]) => Plan;
}

// 穷举所有小表，验证下推前后等价；发现反例立即返回。
export function checkEquivalence(opts: CheckOptions): EquivResult {
  let checked = 0;
  for (const left of enumerateTables(opts.leftCols, opts.domain, opts.maxRows)) {
    for (const right of enumerateTables(opts.rightCols, opts.domain, opts.maxRows)) {
      const before = execute(opts.makeBefore(left, right));
      const after = execute(opts.makeAfter(left, right));
      checked++;
      if (!sameMultiset(before, after)) {
        return {
          equivalent: false,
          checkedCases: checked,
          counterexample: { left, right, before, after },
        };
      }
    }
  }
  return { equivalent: true, checkedCases: checked };
}
