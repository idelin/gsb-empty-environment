export type ParseErrorCode = 'empty-input' | 'unexpected-char' | 'truncated'

export class DecimalError extends Error {}

export class DecimalParseError extends DecimalError {
  readonly pos: number
  readonly code: ParseErrorCode
  readonly char: string | undefined

  constructor(message: string, pos: number, code: ParseErrorCode, char?: string) {
    super(message)
    this.name = 'DecimalParseError'
    this.pos = pos
    this.code = code
    this.char = char
  }
}

export type BudgetKind = 'digits' | 'exponent' | 'output'

export class BudgetExceededError extends DecimalError {
  readonly pos: number
  readonly budget: BudgetKind
  readonly limit: number

  constructor(message: string, pos: number, budget: BudgetKind, limit: number) {
    super(message)
    this.name = 'BudgetExceededError'
    this.pos = pos
    this.budget = budget
    this.limit = limit
  }
}

export class IntervalViolationError extends DecimalError {
  readonly bound: 'min' | 'max'
  readonly inclusive: boolean
  readonly value: string
  readonly limit: string

  constructor(bound: 'min' | 'max', inclusive: boolean, value: string, limit: string) {
    super(
      `value ${value} violates ${bound} bound ${limit} (${inclusive ? 'inclusive' : 'exclusive'})`,
    )
    this.name = 'IntervalViolationError'
    this.bound = bound
    this.inclusive = inclusive
    this.value = value
    this.limit = limit
  }
}
