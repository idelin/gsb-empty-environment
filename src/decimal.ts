import { BudgetExceededError, IntervalViolationError } from './errors.ts'

export type RoundingMode =
  | 'down' // toward zero (truncate)
  | 'up' // away from zero
  | 'floor' // toward -Infinity
  | 'ceil' // toward +Infinity
  | 'half-up' // nearest, ties away from zero
  | 'half-down' // nearest, ties toward zero
  | 'half-even' // nearest, ties to even (banker's)

export interface DecimalInit {
  sign: -1 | 0 | 1
  negativeZero: boolean
  coeff: bigint
  exp: number
  sigDigits: number
  trailingZeros: number
  raw: string | null
}

export interface DecimalBudgets {
  maxDigits?: number
  maxExponent?: number
}

export interface FormatOptions {
  format?: 'fixed' | 'scientific' | 'auto'
  maxOutputDigits?: number
}

export interface QuantizeEvidence {
  mode: RoundingMode
  targetExp: number
  inexact: boolean
  keptDigits: string
  discardedDigits: string
  relationToHalf: 'zero' | 'less' | 'equal' | 'greater'
  incremented: boolean
  direction: -1 | 0 | 1
}

export interface QuantizeResult {
  value: Decimal
  evidence: QuantizeEvidence
}

export interface Interval {
  min?: Decimal
  max?: Decimal
  minInclusive?: boolean
  maxInclusive?: boolean
}

const DEFAULT_MAX_OUTPUT_DIGITS = 100_000
const DEFAULT_MAX_DIGITS = 10_000
const DEFAULT_MAX_EXPONENT = 100_000

function digitCount(v: bigint): number {
  return v.toString().length
}

function pow10(n: number): bigint {
  return 10n ** BigInt(n)
}

function countTrailingZeros(s: string): number {
  let count = 0
  for (let i = s.length - 1; i >= 0 && s[i] === '0'; i--) count++
  return count
}

export class Decimal {
  readonly sign: -1 | 0 | 1
  readonly negativeZero: boolean
  readonly coeff: bigint
  readonly exp: number
  readonly sigDigits: number
  readonly trailingZeros: number
  readonly maxDigits: number
  readonly maxExponent: number
  private readonly _raw: string | null

  constructor(init: DecimalInit, budgets: DecimalBudgets = {}) {
    this.sign = init.sign
    this.negativeZero = init.negativeZero
    this.coeff = init.coeff
    this.exp = init.exp
    this.sigDigits = init.sigDigits
    this.trailingZeros = init.trailingZeros
    this._raw = init.raw
    this.maxDigits = budgets.maxDigits ?? DEFAULT_MAX_DIGITS
    this.maxExponent = budgets.maxExponent ?? DEFAULT_MAX_EXPONENT
  }

  get raw(): string {
    return this._raw ?? this.toString()
  }

  isZero(): boolean {
    return this.sign === 0
  }

  isNegativeZero(): boolean {
    return this.sign === 0 && this.negativeZero
  }

  adjustedExponent(): number {
    if (this.sign === 0) return this.exp
    return this.exp + digitCount(this.coeff) - 1
  }

  private normalized(): { c: bigint; e: number } {
    const s = this.coeff.toString()
    const tz = countTrailingZeros(s)
    if (tz === 0) return { c: this.coeff, e: this.exp }
    return { c: BigInt(s.slice(0, s.length - tz) || '0'), e: this.exp + tz }
  }

  compare(other: Decimal): -1 | 0 | 1 {
    if (this.sign !== other.sign) return this.sign < other.sign ? -1 : 1
    if (this.sign === 0) return 0
    const a = this.normalized()
    const b = other.normalized()
    const adjA = a.e + digitCount(a.c)
    const adjB = b.e + digitCount(b.c)
    let mag: number
    if (adjA !== adjB) {
      mag = adjA < adjB ? -1 : 1
    } else if (a.e === b.e) {
      mag = a.c < b.c ? -1 : a.c > b.c ? 1 : 0
    } else if (a.e > b.e) {
      const scaled = a.c * pow10(a.e - b.e)
      mag = scaled < b.c ? -1 : scaled > b.c ? 1 : 0
    } else {
      const scaled = b.c * pow10(b.e - a.e)
      mag = a.c < scaled ? -1 : a.c > scaled ? 1 : 0
    }
    const signed = this.sign === 1 ? mag : -mag
    return (signed === 0 ? 0 : signed) as -1 | 0 | 1
  }

  equals(other: Decimal): boolean {
    return this.compare(other) === 0
  }

  identical(other: Decimal): boolean {
    return (
      this.compare(other) === 0 &&
      this.negativeZero === other.negativeZero &&
      this.sigDigits === other.sigDigits &&
      this.trailingZeros === other.trailingZeros &&
      this.exp === other.exp
    )
  }

  quantize(targetExp: number, mode: RoundingMode = 'half-even'): QuantizeResult {
    if (!Number.isInteger(targetExp)) {
      throw new RangeError(`targetExp must be an integer, got ${targetExp}`)
    }
    if (Math.abs(targetExp) > this.maxExponent) {
      throw new BudgetExceededError(
        `target exponent ${targetExp} exceeds budget ${this.maxExponent}`,
        0,
        'exponent',
        this.maxExponent,
      )
    }

    const k = targetExp - this.exp
    let newCoeff: bigint
    let evidence: QuantizeEvidence

    if (k <= 0) {
      const shift = -k
      if (digitCount(this.coeff) + shift > this.maxDigits) {
        throw new BudgetExceededError(
          `quantize shift ${shift} exceeds digit budget ${this.maxDigits}`,
          0,
          'digits',
          this.maxDigits,
        )
      }
      newCoeff = this.coeff * pow10(shift)
      evidence = {
        mode,
        targetExp,
        inexact: false,
        keptDigits: this.coeff.toString(),
        discardedDigits: '',
        relationToHalf: 'zero',
        incremented: false,
        direction: 0,
      }
    } else {
      const digits = digitCount(this.coeff)
      let q: bigint
      let r: bigint
      let relation: QuantizeEvidence['relationToHalf']
      if (k > digits) {
        q = 0n
        r = this.coeff
        relation = this.coeff === 0n ? 'zero' : 'less'
      } else {
        const p = pow10(k)
        q = this.coeff / p
        r = this.coeff % p
        if (r === 0n) relation = 'zero'
        else {
          const twice = r * 2n
          relation = twice < p ? 'less' : twice === p ? 'equal' : 'greater'
        }
      }
      const inexact = r !== 0n
      let inc: boolean
      switch (mode) {
        case 'down':
          inc = false
          break
        case 'up':
          inc = inexact
          break
        case 'floor':
          inc = inexact && this.sign === -1
          break
        case 'ceil':
          inc = inexact && this.sign === 1
          break
        case 'half-up':
          inc = relation === 'equal' || relation === 'greater'
          break
        case 'half-down':
          inc = relation === 'greater'
          break
        case 'half-even':
          inc = relation === 'greater' || (relation === 'equal' && q % 2n === 1n)
          break
      }
      newCoeff = inc ? q + 1n : q
      const direction: -1 | 0 | 1 = !inexact ? 0 : ((inc ? 1 : -1) * (this.sign === 0 ? 1 : this.sign)) as -1 | 0 | 1
      evidence = {
        mode,
        targetExp,
        inexact,
        keptDigits: q.toString(),
        discardedDigits: r.toString().padStart(k, '0'),
        relationToHalf: relation,
        incremented: inc,
        direction,
      }
    }

    const resultSign: -1 | 0 | 1 = newCoeff === 0n ? 0 : this.sign === 0 ? 1 : this.sign
    const resultNegativeZero =
      newCoeff === 0n && (this.negativeZero || this.sign === -1)
    const coeffStr = newCoeff.toString()
    const value = new Decimal(
      {
        sign: resultSign,
        negativeZero: resultNegativeZero,
        coeff: newCoeff,
        exp: targetExp,
        sigDigits: newCoeff === 0n ? (targetExp < 0 ? -targetExp : 1) : coeffStr.length,
        trailingZeros:
          newCoeff === 0n ? (targetExp < 0 ? -targetExp : 0) : countTrailingZeros(coeffStr),
        raw: null,
      },
      { maxDigits: this.maxDigits, maxExponent: this.maxExponent },
    )
    return { value, evidence }
  }

  assertInInterval(interval: Interval): this {
    const minInclusive = interval.minInclusive ?? true
    const maxInclusive = interval.maxInclusive ?? true
    if (interval.min !== undefined) {
      const c = this.compare(interval.min)
      if (c < 0 || (c === 0 && !minInclusive)) {
        throw new IntervalViolationError('min', minInclusive, this.toString(), interval.min.toString())
      }
    }
    if (interval.max !== undefined) {
      const c = this.compare(interval.max)
      if (c > 0 || (c === 0 && !maxInclusive)) {
        throw new IntervalViolationError('max', maxInclusive, this.toString(), interval.max.toString())
      }
    }
    return this
  }

  toString(options: FormatOptions = {}): string {
    const format = options.format ?? 'auto'
    const maxOutput = options.maxOutputDigits ?? DEFAULT_MAX_OUTPUT_DIGITS
    let body: string
    if (format === 'scientific') {
      body = this.toScientific()
    } else if (format === 'fixed') {
      body = this.toFixed()
    } else {
      const adj = this.adjustedExponent()
      body = this.sign !== 0 && (adj < -6 || adj >= 21) ? this.toScientific() : this.toFixed()
    }
    if (body.length > maxOutput) {
      throw new BudgetExceededError(
        `formatted output of ${body.length} chars exceeds budget ${maxOutput}`,
        0,
        'output',
        maxOutput,
      )
    }
    return body
  }

  private signPrefix(): string {
    return this.sign === -1 || this.negativeZero ? '-' : ''
  }

  private toFixed(): string {
    const neg = this.signPrefix()
    if (this.sign === 0) {
      return this.exp < 0 ? `${neg}0.${'0'.repeat(-this.exp)}` : `${neg}0`
    }
    const s = this.coeff.toString()
    const point = s.length + this.exp
    if (point <= 0) return `${neg}0.${'0'.repeat(-point)}${s}`
    if (point >= s.length) return `${neg}${s}${'0'.repeat(point - s.length)}`
    return `${neg}${s.slice(0, point)}.${s.slice(point)}`
  }

  private toScientific(): string {
    const neg = this.signPrefix()
    if (this.sign === 0) {
      const mantissa = this.exp < 0 ? `0.${'0'.repeat(-this.exp)}` : '0'
      return `${neg}${mantissa}e+0`
    }
    const s = this.coeff.toString()
    const adj = this.exp + s.length - 1
    const mantissa = s.length > 1 ? `${s[0]}.${s.slice(1)}` : s
    return `${neg}${mantissa}e${adj < 0 ? '-' : '+'}${Math.abs(adj)}`
  }

  snapshot(): Record<string, unknown> {
    return {
      raw: this._raw,
      sign: this.sign,
      negativeZero: this.negativeZero,
      coeff: this.coeff.toString(),
      exp: this.exp,
      sigDigits: this.sigDigits,
      trailingZeros: this.trailingZeros,
      canonical: this.toString(),
    }
  }
}
