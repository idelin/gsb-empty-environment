import { Row } from './ast';
import { RowPredicate } from './compiler';
import { TypeErrorInfo } from './result';

export type FilterOutcome =
  | { ok: true; rows: Row[] }
  | { ok: false; error: TypeErrorInfo; rowIndex: number };

export function filterRows(rows: readonly Row[], predicate: RowPredicate): FilterOutcome {
  const selected: Row[] = [];
  for (let i = 0; i < rows.length; i++) {
    const row = rows[i]!;
    const result = predicate(row);
    if (!result.ok) return { ok: false, error: result.error, rowIndex: i };
    if (result.value === 'true') selected.push(row);
  }
  return { ok: true, rows: selected };
}
