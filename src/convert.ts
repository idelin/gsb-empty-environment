/**
 * Top-level numeral parsing / formatting / base conversion with an
 * explicit workload + budget report.
 *
 * Numeral grammar (per base, case-sensitive digits):
 *   numeral := [-] int [ "." frac [ "(" cycle ")" ] ]
 * Examples: "-1.0(01)" (binary), "0.(3)" (decimal), "ff.8" (hex).
 *
 * Negative-zero rule: "-0", "-0.0", "-0.(0)" all parse to canonical 0/1
 * and format back as "0" — a minus sign is emitted only for values < 0.
 */

import { resolveCharset } from "./charset.ts";
import { digitsToInt, intToDigits, InvalidDigitError, type Workload } from "./integer.ts";
import { FractionExpander } from "./fraction.ts";
import { Rational } from "./rational.ts";

export type RoundingMode = "truncate" | "nearest";

export interface FormatOptions {
  base: number;
  charset?: string;
  /** Max fractional digits to emit. Default: unbounded (always terminates or finds the cycle). */
  budget?: number;
  /** Rounding applied ONLY when the budget truncates the output. Default 'truncate'. */
  rounding?: RoundingMode;
}

export interface ConversionReport {
  outputBase: number;
  integerDivisions: number;
  fractionSteps: number;
  budget: number;
  budgetUsed: number;
  digitsEmitted: number;
  status: "terminated" | "cycle" | "budget-exhausted" | "integer";
  complete: boolean;
  rounded: boolean;
  rounding: RoundingMode;
}

export interface FormatResult {
  /** Pure numeral text, e.g. "-1.0(01)". Never "-0". */
  text: string;
  intDigits: string;
  prefix: string;
  cycle: string;
  negative: boolean;
  complete: boolean;
  report: ConversionReport;
}

// Grammar: [-] int [ "." frac [ "(" cycle ")" ] ]
//          frac may be empty ONLY when a cycle follows: "0.(3)" is legal,
//          a bare trailing dot ("1.") is not.
const NUMERAL_RE = /^(-?)([^.()]+)(?:\.([^.()]*)(?:\(([^.()]+)\))?)?$/;

export function parseRational(text: string, base: number, charset = "lower"): Rational {
  const cs = resolveCharset(charset, base);
  const m = NUMERAL_RE.exec(text);
  if (!m) throw new SyntaxError(`malformed numeral for base ${base}: ${JSON.stringify(text)}`);
  const [, sign, intPart, fracPart, cyclePart] = m;
  if (fracPart !== undefined && fracPart.length === 0 && cyclePart === undefined) {
    throw new SyntaxError(`malformed numeral for base ${base}: ${JSON.stringify(text)}`);
  }
  const workload: Workload = { divisions: 0, multiplyAdds: 0 };

  let pos = sign.length;
  const intVal = digitsToInt(intPart, base, cs, workload, pos);
  pos += intPart.length;

  let num = intVal;
  let den = 1n;
  const b = BigInt(base);
  if (fracPart !== undefined) {
    pos += 1; // '.'
    const fracVal = fracPart.length > 0 ? digitsToInt(fracPart, base, cs, workload, pos) : 0n;
    pos += fracPart.length;
    const p = BigInt(fracPart.length);
    if (cyclePart !== undefined) {
      pos += 1; // '('
      const cycVal = cyclePart.length > 0 ? digitsToInt(cyclePart, base, cs, workload, pos) : 0n;
      const c = BigInt(cyclePart.length);
      // value = int + frac/b^p + cyc/(b^p (b^c - 1))
      const bc = b ** c;
      num = intVal * b ** p * (bc - 1n) + fracVal * (bc - 1n) + cycVal;
      den = b ** p * (bc - 1n);
    } else {
      num = intVal * b ** p + fracVal;
      den = b ** p;
    }
  }
  const r = new Rational(sign === "-" ? -num : num, den);
  return r; // constructor normalizes negative zero
}

/** Round-half-up carry over a digit string; may grow the integer part. */
function roundUp(
  intDigits: string,
  fracDigits: string,
  base: number,
  cs: string,
): { intDigits: string; fracDigits: string } {
  const inc = (s: string): { out: string; carry: boolean } => {
    const arr = s.split("");
    for (let i = arr.length - 1; i >= 0; i--) {
      const v = cs.indexOf(arr[i]);
      if (v + 1 < base) {
        arr[i] = cs[v + 1];
        return { out: arr.join(""), carry: false };
      }
      arr[i] = cs[0];
    }
    return { out: arr.join(""), carry: true };
  };
  let f = fracDigits;
  let i = intDigits;
  if (f.length > 0) {
    const r = inc(f);
    f = r.out;
    if (!r.carry) return { intDigits: i, fracDigits: f };
  }
  const r = inc(i);
  i = r.carry ? cs[1] + r.out : r.out;
  return { intDigits: i, fracDigits: f };
}

export function formatRational(value: Rational, opts: FormatOptions): FormatResult {
  const cs = resolveCharset(opts.charset ?? "lower", opts.base);
  const budget = opts.budget ?? Infinity;
  const rounding = opts.rounding ?? "truncate";
  const negative = value.isNegative; // false for zero: negative-zero rule
  const mag = new Rational(negative ? -value.num : value.num, value.den);
  const { int, rem, den } = mag.split();

  const workload: Workload = { divisions: 0, multiplyAdds: 0 };
  let intDigits = intToDigits(int, opts.base, cs, workload);

  const ex = new FractionExpander(rem, den, opts.base, cs, budget);
  ex.run();
  const status = ex.getStatus();

  let prefix = ex.getPrefix();
  let cycle = ex.getCycle();
  let rounded = false;

  if (status === "budget-exhausted" && rounding === "nearest") {
    // Peek one digit past the cut (lookahead is not counted against the budget).
    const peek = new FractionExpander(rem, den, opts.base, cs, budget + 1);
    peek.run();
    const next = peek.getDigits()[budget];
    if (next !== undefined && 2 * cs.indexOf(next) >= opts.base) {
      const r = roundUp(intDigits, prefix, opts.base, cs);
      intDigits = r.intDigits;
      prefix = r.fracDigits;
      rounded = true;
    }
  }

  const complete = status !== "budget-exhausted";
  let fracText = "";
  if (prefix.length > 0 || cycle.length > 0) {
    fracText = "." + prefix + (cycle.length > 0 ? `(${cycle})` : "");
  }
  const text = (negative ? "-" : "") + intDigits + fracText;

  const report: ConversionReport = {
    outputBase: opts.base,
    integerDivisions: workload.divisions,
    fractionSteps: ex.getSteps(),
    budget,
    budgetUsed: ex.getSteps(),
    digitsEmitted: intDigits.length + prefix.length + cycle.length,
    status: status === "running" || status === "paused" ? "integer" : status,
    complete,
    rounded,
    rounding,
  };
  return { text, intDigits, prefix, cycle, negative, complete, report };
}

export interface ConvertOptions extends FormatOptions {
  fromBase: number;
  fromCharset?: string;
}

/** Full pipeline: parse in fromBase, re-format in opts.base, with report. */
export function convert(text: string, opts: ConvertOptions): FormatResult & { value: Rational } {
  const value = parseRational(text, opts.fromBase, opts.fromCharset ?? "lower");
  const out = formatRational(value, opts);
  return { ...out, value };
}

export { InvalidDigitError };
