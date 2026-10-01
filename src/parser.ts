import { DecimalParseError } from './errors.ts';
import { makeDecimal, type Decimal } from './decimal.ts';

/**
 * Strict grammar (no whitespace, no grouping separators, no hex/Infinity/NaN):
 *
 *   number   := sign? mantissa exponent?
 *   sign     := '+' | '-'
 *   mantissa := digits '.' digits | digits | '.' digits
 *   exponent := ('e' | 'E') sign? digits
 *
 * Examples accepted: 0, -0, +1.5, .5, 1.0, 1e10, 1E-10, -2.5e+3
 * Examples rejected: '', '1.', '1e', '1e+', '1,000', '1_000', ' 1', '1 ',
 *                    '--1', '1..2', '1e2e3', 'abc'
 */
export interface ParserOptions {
  /** Maximum number of mantissa digits (integer + fraction). */
  maxMantissaDigits?: number;
  /** Maximum absolute value of the written exponent. Checked while scanning,
   *  before any use of the exponent, so huge exponents are rejected first. */
  maxExponent?: number;
  /** Maximum number of exponent digits. */
  maxExponentDigits?: number;
}

const DEFAULTS: Required<ParserOptions> = {
  maxMantissaDigits: 100_000,
  maxExponent: 999_999,
  maxExponentDigits: 7,
};

type State = 'start' | 'sign' | 'int' | 'dot' | 'dotInt' | 'frac' | 'eMark' | 'eSign' | 'eDigits';

const FINAL_STATES: ReadonlySet<State> = new Set(['int', 'frac', 'eDigits']);

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

/**
 * Incremental strict-decimal parser. Feed arbitrary chunks with `write()` and
 * finalize with `finish()`. Chunk boundaries are irrelevant: parsing "1.23e4"
 * as ["1.", "23e", "4"] yields exactly the same result as one-shot parsing.
 * All error positions are absolute offsets into the logical input stream.
 */
export class DecimalParser {
  private state: State = 'start';
  private pos = 0;
  private closed = false;
  private negative = false;
  private intDigits = '';
  private fracDigits: string | null = null;
  private expNegative = false;
  private expDigits = '';
  private expValue = 0;
  private raw = '';
  private mantissaCount = 0;
  private readonly opts: Required<ParserOptions>;

  constructor(opts: ParserOptions = {}) {
    this.opts = { ...DEFAULTS, ...opts };
    if (this.opts.maxExponent > 10 ** this.opts.maxExponentDigits - 1) {
      throw new DecimalParseError(
        'BUDGET_EXCEEDED',
        `maxExponent ${this.opts.maxExponent} is unreachable with maxExponentDigits ${this.opts.maxExponentDigits}`,
        -1,
        'config',
      );
    }
  }

  get position(): number {
    return this.pos;
  }

  get currentState(): string {
    return this.state;
  }

  write(chunk: string): this {
    for (const ch of chunk) this.feed(ch);
    return this;
  }

  finish(): Decimal {
    if (this.closed) {
      throw new DecimalParseError('PARSER_CLOSED', 'parser is already closed', this.pos, this.state);
    }
    this.closed = true;
    if (this.state === 'start') {
      throw new DecimalParseError('EMPTY_INPUT', 'empty input is not a number', this.pos, this.state);
    }
    if (!FINAL_STATES.has(this.state)) {
      throw new DecimalParseError(
        'TRUNCATED_INPUT',
        `input ended unexpectedly in state '${this.state}'`,
        this.pos,
        this.state,
      );
    }
    return makeDecimal({
      negative: this.negative,
      intDigits: this.intDigits,
      fracDigits: this.fracDigits,
      exponentValue: this.expNegative ? -this.expValue : this.expValue,
      raw: this.raw,
    });
  }

  private fail(code: 'INVALID_CHARACTER' | 'INVALID_EXPONENT' | 'BUDGET_EXCEEDED' | 'EXPONENT_OVERFLOW', message: string): never {
    this.closed = true;
    throw new DecimalParseError(code, message, this.pos, this.state);
  }

  private feed(ch: string): void {
    if (this.closed) {
      throw new DecimalParseError('PARSER_CLOSED', 'parser is already closed', this.pos, this.state);
    }
    const digit = isDigit(ch);
    switch (this.state) {
      case 'start':
        if (ch === '+' || ch === '-') {
          this.negative = ch === '-';
          this.state = 'sign';
        } else if (digit) {
          this.pushMantissaDigit(ch, 'int');
        } else if (ch === '.') {
          this.fracDigits = '';
          this.state = 'dot';
        } else {
          this.fail('INVALID_CHARACTER', `unexpected character '${ch}' at start of number`);
        }
        break;
      case 'sign':
        if (digit) {
          this.pushMantissaDigit(ch, 'int');
        } else if (ch === '.') {
          this.fracDigits = '';
          this.state = 'dot';
        } else {
          this.fail('INVALID_CHARACTER', `unexpected character '${ch}' after sign`);
        }
        break;
      case 'int':
        if (digit) {
          this.pushMantissaDigit(ch, 'int');
        } else if (ch === '.') {
          this.fracDigits = '';
          this.state = 'dotInt';
        } else if (ch === 'e' || ch === 'E') {
          this.state = 'eMark';
        } else {
          this.fail('INVALID_CHARACTER', `unexpected character '${ch}' in integer part`);
        }
        break;
      case 'dot':
      case 'dotInt':
        if (digit) {
          this.pushMantissaDigit(ch, 'frac');
        } else {
          this.fail('INVALID_CHARACTER', `unexpected character '${ch}' after decimal point`);
        }
        break;
      case 'frac':
        if (digit) {
          this.pushMantissaDigit(ch, 'frac');
        } else if (ch === 'e' || ch === 'E') {
          this.state = 'eMark';
        } else {
          this.fail('INVALID_CHARACTER', `unexpected character '${ch}' in fraction`);
        }
        break;
      case 'eMark':
        if (digit) {
          this.pushExponentDigit(ch);
        } else if (ch === '+' || ch === '-') {
          this.expNegative = ch === '-';
          this.state = 'eSign';
        } else {
          this.fail('INVALID_EXPONENT', `unexpected character '${ch}' in exponent`);
        }
        break;
      case 'eSign':
        if (digit) {
          this.pushExponentDigit(ch);
        } else {
          this.fail('INVALID_EXPONENT', `unexpected character '${ch}' after exponent sign`);
        }
        break;
      case 'eDigits':
        if (digit) {
          this.pushExponentDigit(ch);
        } else {
          this.fail('INVALID_EXPONENT', `unexpected character '${ch}' after exponent`);
        }
        break;
    }
    this.raw += ch;
    this.pos++;
  }

  private pushMantissaDigit(ch: string, part: 'int' | 'frac'): void {
    this.mantissaCount++;
    if (this.mantissaCount > this.opts.maxMantissaDigits) {
      this.fail(
        'BUDGET_EXCEEDED',
        `mantissa digit budget ${this.opts.maxMantissaDigits} exceeded`,
      );
    }
    if (part === 'int') {
      this.intDigits += ch;
      this.state = 'int';
    } else {
      this.fracDigits = (this.fracDigits ?? '') + ch;
      this.state = 'frac';
    }
  }

  private pushExponentDigit(ch: string): void {
    if (this.expDigits.length >= this.opts.maxExponentDigits) {
      this.fail(
        'EXPONENT_OVERFLOW',
        `exponent digit budget ${this.opts.maxExponentDigits} exceeded`,
      );
    }
    this.expValue = this.expValue * 10 + (ch.charCodeAt(0) - 48);
    if (this.expValue > this.opts.maxExponent) {
      this.fail(
        'EXPONENT_OVERFLOW',
        `exponent magnitude exceeds budget ${this.opts.maxExponent}`,
      );
    }
    this.expDigits += ch;
    this.state = 'eDigits';
  }
}

/** One-shot strict parse. */
export function parseDecimal(input: string, opts: ParserOptions = {}): Decimal {
  return new DecimalParser(opts).write(input).finish();
}
