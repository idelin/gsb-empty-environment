import { ParsedRow, RowViolation, ValidationResult } from "./types";

/** Returns an error message, or null when the value is valid. */
export type Rule = (value: string | undefined) => string | null;

export interface Schema {
  [column: string]: Rule[];
}

export const rules = {
  required(message?: string): Rule {
    return (value) =>
      value === undefined || value.trim() === ""
        ? message || "value is required"
        : null;
  },
  pattern(re: RegExp, message?: string): Rule {
    return (value) =>
      value !== undefined && value !== "" && !re.test(value)
        ? message || `value does not match ${re}`
        : null;
  },
  integer(message?: string): Rule {
    return (value) =>
      value !== undefined && value !== "" && !/^-?\d+$/.test(value)
        ? message || "value must be an integer"
        : null;
  },
};

/**
 * Validates each row independently. Rows with violations are isolated into
 * `rejected`; valid rows are unaffected and stay in original order.
 */
export function validateRows(rows: ParsedRow[], schema: Schema): ValidationResult {
  const result: ValidationResult = { accepted: [], rejected: [] };
  for (const row of rows) {
    const violations: RowViolation[] = [];
    for (const column of Object.keys(schema)) {
      for (const rule of schema[column]) {
        const message = rule(row.record[column]);
        if (message !== null) {
          violations.push({ line: row.line, field: column, message });
        }
      }
    }
    if (violations.length === 0) {
      result.accepted.push({ line: row.line, record: row.record });
    } else {
      result.rejected.push({ line: row.line, record: row.record, violations });
    }
  }
  return result;
}
