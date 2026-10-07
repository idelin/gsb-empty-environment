import { RawRow, RowError } from './types';

export const EXPECTED_FIELDS = 3;

export interface ParseOutput {
  rows: RawRow[];
  errors: RowError[];
}

/**
 * Parses CSV-ish text (`id,name,quantity` per line, optional header).
 * Malformed lines are isolated as errors and never reach validation.
 */
export function parseRows(source: string): ParseOutput {
  const rows: RawRow[] = [];
  const errors: RowError[] = [];
  const lines = source.split(/\r?\n/);
  lines.forEach((rawLine, index) => {
    const line = index + 1;
    const trimmed = rawLine.trim();
    if (trimmed === '' || trimmed.startsWith('#')) {
      return;
    }
    if (line === 1 && trimmed.toLowerCase().startsWith('id,')) {
      return; // header
    }
    const fields = trimmed.split(',').map((field) => field.trim());
    if (fields.length !== EXPECTED_FIELDS) {
      errors.push({
        line,
        reason: `expected ${EXPECTED_FIELDS} fields, got ${fields.length}`,
        raw: rawLine,
      });
      return;
    }
    rows.push({ line, fields, raw: rawLine });
  });
  return { rows, errors };
}
