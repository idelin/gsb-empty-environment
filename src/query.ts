import {
  effectiveReaders,
  sameSources,
  type CharCell,
  type ReaderId,
  type SourceId,
  type SourceRegistry,
} from './model.ts';

export const DEFAULT_REDACTION_CHAR = '█';

export interface Redaction {
  readonly start: number;
  readonly end: number;
  readonly reasons: SourceId[];
}

export interface QueryResult {
  readonly text: string;
  readonly redactions: Redaction[];
}

/**
 * 查询某读者可见的文本：不可见字符以遮挡字符替代，
 * 并返回合并后的遮挡区间及其原因来源（排除了该读者的来源片段）。
 */
export function queryCells(
  registry: SourceRegistry,
  cells: readonly CharCell[],
  reader: ReaderId,
  redactionChar: string = DEFAULT_REDACTION_CHAR,
): QueryResult {
  let text = '';
  const redactions: Redaction[] = [];
  cells.forEach((cell, i) => {
    const readers = effectiveReaders(registry, cell.sources);
    if (readers === null || readers.has(reader)) {
      text += cell.ch;
      return;
    }
    text += redactionChar;
    const reasons = cell.sources.filter((id) => !registry.readersOf(id).has(reader));
    const last = redactions[redactions.length - 1];
    if (last && last.end === i && sameSources(last.reasons, reasons)) {
      redactions[redactions.length - 1] = { start: last.start, end: i + 1, reasons: last.reasons };
    } else {
      redactions.push({ start: i, end: i + 1, reasons });
    }
  });
  return { text, redactions };
}
