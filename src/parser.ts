import { Decimal } from './decimal.ts'
import { BudgetExceededError, DecimalParseError } from './errors.ts'

export interface ParserOptions {
  maxDigits?: number
  maxExponent?: number
}

const DEFAULT_MAX_DIGITS = 10_000
const DEFAULT_MAX_EXPONENT = 100_000
const MAX_EXPONENT_CAP = Number.MAX_SAFE_INTEGER

type State = 'start' | 'sign' | 'int' | 'dot' | 'frac' | 'expMark' | 'expDigits'

function isDigitChar(ch: string): boolean {
  return ch >= '0' && ch <= '9'
}

export class DecimalParser {
  private state: State = 'start'
  private position = 0
  private rawParts: string[] = []
  private negative = false
  private intDigits = ''
  private fracDigits = ''
  private mantissaDigits = 0
  private expNegative = false
  private expSignSeen = false
  private expBig = 0n
  private expMarkPos = -1
  readonly maxDigits: number
  readonly maxExponent: number

  constructor(options: ParserOptions = {}) {
    this.maxDigits = options.maxDigits ?? DEFAULT_MAX_DIGITS
    this.maxExponent = Math.min(options.maxExponent ?? DEFAULT_MAX_EXPONENT, MAX_EXPONENT_CAP)
    if (!Number.isInteger(this.maxDigits) || this.maxDigits < 1) {
      throw new RangeError(`maxDigits must be a positive integer, got ${this.maxDigits}`)
    }
    if (!Number.isInteger(this.maxExponent) || this.maxExponent < 0) {
      throw new RangeError(`maxExponent must be a non-negative integer, got ${this.maxExponent}`)
    }
  }

  get pos(): number {
    return this.position
  }

  feed(chunk: string): this {
    for (const ch of chunk) this.feedChar(ch)
    return this
  }

  finish(): Decimal {
    switch (this.state) {
      case 'start':
        throw new DecimalParseError('empty input: expected a number at position 0', 0, 'empty-input')
      case 'sign':
        throw new DecimalParseError(
          `truncated input: expected a digit at position ${this.position}`,
          this.position,
          'truncated',
        )
      case 'dot':
        throw new DecimalParseError(
          `truncated input: expected a fraction digit at position ${this.position}`,
          this.position,
          'truncated',
        )
      case 'expMark':
        throw new DecimalParseError(
          `truncated input: expected an exponent digit at position ${this.position}`,
          this.position,
          'truncated',
        )
      case 'int':
      case 'frac':
      case 'expDigits':
        return this.build()
    }
  }

  private feedChar(ch: string): void {
    const p = this.position
    switch (this.state) {
      case 'start':
        if (ch === '+' || ch === '-') {
          this.negative = ch === '-'
          this.state = 'sign'
        } else if (isDigitChar(ch)) {
          this.pushInt(ch, p)
        } else if (ch === '.') {
          this.state = 'dot'
        } else {
          throw this.unexpected(ch, p)
        }
        break
      case 'sign':
        if (isDigitChar(ch)) this.pushInt(ch, p)
        else if (ch === '.') this.state = 'dot'
        else throw this.unexpected(ch, p)
        break
      case 'int':
        if (isDigitChar(ch)) this.pushInt(ch, p)
        else if (ch === '.') this.state = 'dot'
        else if (ch === 'e' || ch === 'E') {
          this.state = 'expMark'
          this.expMarkPos = p
        } else throw this.unexpected(ch, p)
        break
      case 'dot':
        if (isDigitChar(ch)) this.pushFrac(ch, p)
        else throw this.unexpected(ch, p)
        break
      case 'frac':
        if (isDigitChar(ch)) this.pushFrac(ch, p)
        else if (ch === 'e' || ch === 'E') {
          this.state = 'expMark'
          this.expMarkPos = p
        } else throw this.unexpected(ch, p)
        break
      case 'expMark':
        if (isDigitChar(ch)) {
          this.pushExp(ch, p)
        } else if ((ch === '+' || ch === '-') && !this.expSignSeen) {
          this.expSignSeen = true
          this.expNegative = ch === '-'
        } else throw this.unexpected(ch, p)
        break
      case 'expDigits':
        if (isDigitChar(ch)) this.pushExp(ch, p)
        else throw this.unexpected(ch, p)
        break
    }
    this.rawParts.push(ch)
    this.position++
  }

  private unexpected(ch: string, p: number): DecimalParseError {
    return new DecimalParseError(
      `unexpected character ${JSON.stringify(ch)} at position ${p}`,
      p,
      'unexpected-char',
      ch,
    )
  }

  private pushInt(ch: string, p: number): void {
    this.mantissaDigits++
    if (this.mantissaDigits > this.maxDigits) {
      throw new BudgetExceededError(
        `mantissa digit budget ${this.maxDigits} exceeded at position ${p}`,
        p,
        'digits',
        this.maxDigits,
      )
    }
    this.intDigits += ch
    this.state = 'int'
  }

  private pushFrac(ch: string, p: number): void {
    this.mantissaDigits++
    if (this.mantissaDigits > this.maxDigits) {
      throw new BudgetExceededError(
        `mantissa digit budget ${this.maxDigits} exceeded at position ${p}`,
        p,
        'digits',
        this.maxDigits,
      )
    }
    this.fracDigits += ch
    this.state = 'frac'
  }

  private pushExp(ch: string, p: number): void {
    this.expBig = this.expBig * 10n + BigInt(ch)
    this.state = 'expDigits'
    if (this.expBig > BigInt(this.maxExponent)) {
      throw new BudgetExceededError(
        `exponent budget ${this.maxExponent} exceeded at position ${p}`,
        p,
        'exponent',
        this.maxExponent,
      )
    }
  }

  private build(): Decimal {
    const expParsed = Number(this.expBig) * (this.expNegative ? -1 : 1)
    const combined = this.intDigits + this.fracDigits
    let firstNz = -1
    for (let i = 0; i < combined.length; i++) {
      if (combined[i] !== '0') {
        firstNz = i
        break
      }
    }
    const raw = this.rawParts.join('')
    const budgets = { maxDigits: this.maxDigits, maxExponent: this.maxExponent }

    if (firstNz === -1) {
      const fracLen = this.fracDigits.length
      return new Decimal(
        {
          sign: 0,
          negativeZero: this.negative,
          coeff: 0n,
          exp: fracLen > 0 ? -fracLen : 0,
          sigDigits: fracLen > 0 ? fracLen : 1,
          trailingZeros: fracLen,
          raw,
        },
        budgets,
      )
    }

    const adjusted = expParsed + this.intDigits.length - firstNz - 1
    if (Math.abs(adjusted) > this.maxExponent) {
      throw new BudgetExceededError(
        `adjusted exponent ${adjusted} exceeds budget ${this.maxExponent}`,
        this.expMarkPos >= 0 ? this.expMarkPos : 0,
        'exponent',
        this.maxExponent,
      )
    }

    const sig = combined.slice(firstNz)
    let trailingZeros = 0
    for (let i = sig.length - 1; i >= 0 && sig[i] === '0'; i--) trailingZeros++

    return new Decimal(
      {
        sign: this.negative ? -1 : 1,
        negativeZero: false,
        coeff: BigInt(sig),
        exp: expParsed - this.fracDigits.length,
        sigDigits: sig.length,
        trailingZeros,
        raw,
      },
      budgets,
    )
  }
}

export function parseDecimal(input: string, options: ParserOptions = {}): Decimal {
  return new DecimalParser(options).feed(input).finish()
}
