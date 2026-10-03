/**
 * Validation against the implemented schema subset. Every error carries
 * both an instance path (where the value lives) and a rule path (which
 * keyword failed). No third-party validator is involved.
 */

import type { Decimal, DecimalOptions } from "./decimal.ts";
import {
  DecimalBudgetError,
  DecimalParseError,
  compare,
  fractionDigits,
  isMultipleOf,
  parseDecimal,
  toPlainString,
  totalDigits,
} from "./decimal.ts";
import { compileSchema } from "./schema.ts";
import type { CompiledSchema, DecimalSchema } from "./schema.ts";

export interface ValidationError {
  /** JSON Pointer to the offending value ("" for the root scalar). */
  instancePath: string;
  /** JSON Pointer to the violated keyword inside the schema. */
  rulePath: string;
  keyword: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  errors: ValidationError[];
}

export interface ValidateOptions extends DecimalOptions {
  instancePath?: string;
}

/** Evaluates an already-parsed decimal against a compiled schema. */
export function evaluate(value: Decimal, compiled: CompiledSchema, instancePath = ""): ValidationError[] {
  const errors: ValidationError[] = [];
  const push = (keyword: string, message: string): void => {
    errors.push({ instancePath, rulePath: `/${keyword}`, keyword, message });
  };
  const shown = toPlainString(value);
  if (compiled.lo) {
    const cmp = compare(value, compiled.lo.value);
    if (cmp < 0 || (cmp === 0 && !compiled.lo.inclusive)) {
      push(compiled.lo.keyword, `${shown} is below ${compiled.lo.keyword} ${toPlainString(compiled.lo.value)}`);
    }
  }
  if (compiled.hi) {
    const cmp = compare(value, compiled.hi.value);
    if (cmp > 0 || (cmp === 0 && !compiled.hi.inclusive)) {
      push(compiled.hi.keyword, `${shown} is above ${compiled.hi.keyword} ${toPlainString(compiled.hi.value)}`);
    }
  }
  if (compiled.multipleOf && !isMultipleOf(value, compiled.multipleOf)) {
    push("multipleOf", `${shown} is not an integer multiple of ${toPlainString(compiled.multipleOf)}`);
  }
  if (compiled.maxTotalDigits !== undefined && totalDigits(value) > compiled.maxTotalDigits) {
    push(
      "maxTotalDigits",
      `${shown} uses ${totalDigits(value)} total digits, more than maxTotalDigits ${compiled.maxTotalDigits}`,
    );
  }
  if (compiled.maxFractionDigits !== undefined && fractionDigits(value) > compiled.maxFractionDigits) {
    push(
      "maxFractionDigits",
      `${shown} uses ${fractionDigits(value)} fraction digits, more than maxFractionDigits ${compiled.maxFractionDigits}`,
    );
  }
  return errors;
}

export function validate(input: string, schema: DecimalSchema, options: ValidateOptions = {}): ValidationResult {
  const compiled = compileSchema(schema, options);
  const instancePath = options.instancePath ?? "";
  let value: Decimal;
  try {
    value = parseDecimal(input, options);
  } catch (err) {
    if (err instanceof DecimalParseError || err instanceof DecimalBudgetError) {
      return {
        valid: false,
        errors: [{ instancePath, rulePath: "/type", keyword: "decimal", message: err.message }],
      };
    }
    throw err;
  }
  const errors = evaluate(value, compiled, instancePath);
  return { valid: errors.length === 0, errors };
}

/** Containment check: does the value belong to the set described by the schema? */
export function contains(schema: DecimalSchema, input: string, options: ValidateOptions = {}): boolean {
  return validate(input, schema, options).valid;
}
