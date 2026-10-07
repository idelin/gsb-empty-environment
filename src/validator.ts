import { RawRow, RowError, ValidRow } from './types';

export interface ValidateOutput {
  valid: ValidRow[];
  errors: RowError[];
}

/**
 * Field-level validation. Bad rows are isolated into `errors`;
 * good rows keep flowing. Duplicate ids within one source are rejected
 * after their first occurrence.
 */
export function validateRows(rows: RawRow[]): ValidateOutput {
  const valid: ValidRow[] = [];
  const errors: RowError[] = [];
  const seenIds = new Set<string>();
  for (const row of rows) {
    const [id, name, quantityRaw] = row.fields;
    const fail = (reason: string) => errors.push({ line: row.line, reason, raw: row.raw });
    if (!id) {
      fail('id is empty');
      continue;
    }
    if (seenIds.has(id)) {
      fail(`duplicate id "${id}"`);
      continue;
    }
    if (!name) {
      fail('name is empty');
      continue;
    }
    const quantity = Number(quantityRaw);
    if (!Number.isInteger(quantity) || quantity <= 0) {
      fail(`quantity "${quantityRaw}" is not a positive integer`);
      continue;
    }
    seenIds.add(id);
    valid.push({ line: row.line, id, name, quantity });
  }
  return { valid, errors };
}
