import { sameSources, type CharCell, type ReaderId } from './model.ts';
import type { QueryResult } from './query.ts';

export interface Expandable {
  expand(): CharCell[];
  queryReader(reader: ReaderId, redactionChar?: string): QueryResult;
}

function describeCell(cell: CharCell): string {
  return `{ ch: ${JSON.stringify(cell.ch)}, sources: [${cell.sources.join(', ')}] }`;
}

/** 比较两个逐字符展开视图，返回不一致之处的描述列表（空数组 = 一致）。 */
export function diffExpansions(
  actual: readonly CharCell[],
  expected: readonly CharCell[],
): string[] {
  const problems: string[] = [];
  if (actual.length !== expected.length) {
    problems.push(`length mismatch: ${actual.length} !== ${expected.length}`);
  }
  const n = Math.min(actual.length, expected.length);
  for (let i = 0; i < n; i++) {
    const a = actual[i];
    const b = expected[i];
    if (a.ch !== b.ch || !sameSources(a.sources, b.sources)) {
      problems.push(`cell ${i}: ${describeCell(a)} !== ${describeCell(b)}`);
    }
  }
  return problems;
}

/** 比较两次读者查询结果，返回不一致之处的描述列表（空数组 = 一致）。 */
export function diffQueries(actual: QueryResult, expected: QueryResult): string[] {
  const problems: string[] = [];
  if (actual.text !== expected.text) {
    problems.push(`text mismatch: ${JSON.stringify(actual.text)} !== ${JSON.stringify(expected.text)}`);
  }
  if (actual.redactions.length !== expected.redactions.length) {
    problems.push(
      `redaction count mismatch: ${actual.redactions.length} !== ${expected.redactions.length}`,
    );
  }
  const n = Math.min(actual.redactions.length, expected.redactions.length);
  for (let i = 0; i < n; i++) {
    const a = actual.redactions[i];
    const b = expected.redactions[i];
    if (a.start !== b.start || a.end !== b.end || !sameSources(a.reasons, b.reasons)) {
      problems.push(
        `redaction ${i}: [${a.start}, ${a.end}) reasons [${a.reasons}] !== ` +
          `[${b.start}, ${b.end}) reasons [${b.reasons}]`,
      );
    }
  }
  return problems;
}

/**
 * 用逐字符来源集合参考实现验证高效实现：
 * 逐字符比较文本与来源集合，并对每个给定读者比较查询结果（遮挡区间与原因来源）。
 * 不一致时抛出带定位信息的错误。
 */
export function verifyAgainstReference(
  actual: Expandable,
  expected: Expandable,
  readers: Iterable<ReaderId>,
): void {
  const expansionProblems = diffExpansions(actual.expand(), expected.expand());
  if (expansionProblems.length > 0) {
    throw new Error(`expansion mismatch:\n${expansionProblems.join('\n')}`);
  }
  for (const reader of readers) {
    const queryProblems = diffQueries(actual.queryReader(reader), expected.queryReader(reader));
    if (queryProblems.length > 0) {
      throw new Error(`query mismatch for reader "${reader}":\n${queryProblems.join('\n')}`);
    }
  }
}
