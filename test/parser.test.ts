import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  BudgetExceededError,
  DecimalParseError,
  parseDecimal,
} from '../src/index.ts'

describe('strict parsing: valid forms', () => {
  const cases: Array<[string, Record<string, unknown>]> = [
    ['0', { sign: 0, negativeZero: false, coeff: '0', exp: 0, sigDigits: 1, trailingZeros: 0 }],
    ['-0', { sign: 0, negativeZero: true, coeff: '0', exp: 0, sigDigits: 1, trailingZeros: 0 }],
    ['-0.00', { sign: 0, negativeZero: true, coeff: '0', exp: -2, sigDigits: 2, trailingZeros: 2 }],
    ['+1.5', { sign: 1, coeff: '15', exp: -1, sigDigits: 2, trailingZeros: 0 }],
    ['1.2300', { sign: 1, coeff: '12300', exp: -4, sigDigits: 5, trailingZeros: 2 }],
    ['1.23', { sign: 1, coeff: '123', exp: -2, sigDigits: 3, trailingZeros: 0 }],
    ['100', { sign: 1, coeff: '100', exp: 0, sigDigits: 3, trailingZeros: 2 }],
    ['007', { sign: 1, coeff: '7', exp: 0, sigDigits: 1, trailingZeros: 0 }],
    ['.5', { sign: 1, coeff: '5', exp: -1, sigDigits: 1, trailingZeros: 0 }],
    ['-.05', { sign: -1, coeff: '5', exp: -2, sigDigits: 1, trailingZeros: 0 }],
    ['1.23e4', { sign: 1, coeff: '123', exp: 2, sigDigits: 3, trailingZeros: 0 }],
    ['1.23E-4', { sign: 1, coeff: '123', exp: -6, sigDigits: 3, trailingZeros: 0 }],
    ['-2.5e+3', { sign: -1, coeff: '25', exp: 2, sigDigits: 2, trailingZeros: 0 }],
    ['0.0012300', { sign: 1, coeff: '12300', exp: -7, sigDigits: 5, trailingZeros: 2 }],
    ['9e0', { sign: 1, coeff: '9', exp: 0, sigDigits: 1, trailingZeros: 0 }],
  ]
  for (const [input, expected] of cases) {
    it(`parses ${JSON.stringify(input)}`, () => {
      const d = parseDecimal(input)
      const snap = d.snapshot()
      for (const [key, value] of Object.entries(expected)) {
        assert.deepEqual(snap[key], value, `field ${key}`)
      }
      assert.equal(d.raw, input, 'raw lexeme preserved')
    })
  }
})

describe('strict parsing: rejected forms with exact positions', () => {
  const cases: Array<[string, number, string]> = [
    ['1,000', 1, 'unexpected-char'], // grouping commas rejected
    ['1 000', 1, 'unexpected-char'],
    ['1_000', 1, 'unexpected-char'],
    ['1.2.3', 3, 'unexpected-char'],
    ['0x10', 1, 'unexpected-char'],
    ['Infinity', 0, 'unexpected-char'],
    ['NaN', 0, 'unexpected-char'],
    ['--1', 1, 'unexpected-char'],
    ['1e1.5', 3, 'unexpected-char'], // illegal exponent
    ['1e++1', 3, 'unexpected-char'],
    ['1e-', 3, 'truncated'], // finish truncation
    ['1e', 2, 'truncated'],
    ['1e+', 3, 'truncated'],
    ['1.', 2, 'truncated'],
    ['.', 1, 'truncated'],
    ['-', 1, 'truncated'],
    ['abc', 0, 'unexpected-char'],
  ]
  for (const [input, pos, code] of cases) {
    it(`rejects ${JSON.stringify(input)} at pos ${pos} (${code})`, () => {
      assert.throws(
        () => parseDecimal(input),
        (err: unknown) => {
          assert.ok(err instanceof DecimalParseError)
          assert.equal(err.pos, pos)
          assert.equal(err.code, code)
          return true
        },
      )
    })
  }

  it('rejects empty input at pos 0', () => {
    assert.throws(
      () => parseDecimal(''),
      (err: unknown) => {
        assert.ok(err instanceof DecimalParseError)
        assert.equal(err.pos, 0)
        assert.equal(err.code, 'empty-input')
        return true
      },
    )
  })
})

describe('budgets', () => {
  it('checks huge exponent budget before any expansion', () => {
    const input = `1e${'9'.repeat(500)}`
    assert.throws(
      () => parseDecimal(input),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.budget, 'exponent')
        // 999999 exceeds 100000 already at the 6th exponent digit
        assert.equal(err.pos, 2 + 5)
        return true
      },
    )
  })

  it('reports the exact digit where the exponent budget breaks', () => {
    assert.throws(
      () => parseDecimal('1e101', { maxExponent: 100 }),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.pos, 4) // the '1' in "101" pushes 10 -> 101
        return true
      },
    )
  })

  it('enforces mantissa digit budget at the offending position', () => {
    const input = '1'.repeat(11)
    assert.throws(
      () => parseDecimal(input, { maxDigits: 10 }),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.budget, 'digits')
        assert.equal(err.pos, 10)
        return true
      },
    )
  })

  it('checks adjusted exponent (mantissa shifts the decimal point)', () => {
    assert.throws(
      () => parseDecimal('12345e99999', { maxExponent: 100000 }),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.budget, 'exponent')
        return true
      },
    )
    // 1.2345e5 = 12345e1 -> adjusted exponent 5, within budget
    assert.equal(parseDecimal('12345e1', { maxExponent: 100 }).adjustedExponent(), 5)
  })

  it('accepts values exactly at the budget boundary', () => {
    const d = parseDecimal('9e100000')
    assert.equal(d.adjustedExponent(), 100000)
  })
})

describe('canonical output', () => {
  const cases: Array<[string, string, string, string]> = [
    // input, fixed, scientific, auto
    ['1.2300', '1.2300', '1.2300e+0', '1.2300'],
    ['-0', '-0', '-0e+0', '-0'],
    ['-0.00', '-0.00', '-0.00e+0', '-0.00'],
    ['0.0012300', '0.0012300', '1.2300e-3', '0.0012300'],
    ['1.23e4', '12300', '1.23e+4', '12300'],
    ['1.23e-4', '0.000123', '1.23e-4', '0.000123'],
    ['007', '7', '7e+0', '7'],
    ['1e21', '1' + '0'.repeat(21), '1e+21', '1e+21'],
    ['1e-7', '0.0000001', '1e-7', '1e-7'],
    ['-2.5e+3', '-2500', '-2.5e+3', '-2500'],
  ]
  for (const [input, fixed, scientific, auto] of cases) {
    it(`formats ${input}`, () => {
      const d = parseDecimal(input)
      assert.equal(d.toString({ format: 'fixed' }), fixed)
      assert.equal(d.toString({ format: 'scientific' }), scientific)
      assert.equal(d.toString({ format: 'auto' }), auto)
    })
  }

  it('fixed output respects the output budget', () => {
    const d = parseDecimal('1e1000')
    assert.throws(
      () => d.toString({ format: 'fixed', maxOutputDigits: 100 }),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.budget, 'output')
        return true
      },
    )
  })

  it('round-trips numerically through all forms, identically through scientific', () => {
    for (const input of ['1.2300', '-0.00', '1.23e4', '0.0012300', '9e99', '-2.5e+3']) {
      const d = parseDecimal(input)
      for (const format of ['fixed', 'scientific', 'auto'] as const) {
        const round = parseDecimal(d.toString({ format }))
        assert.ok(d.equals(round), `${input} via ${format}`)
      }
      // scientific keeps the exact significant-digit metadata
      assert.ok(d.identical(parseDecimal(d.toString({ format: 'scientific' }))), input)
    }
  })
})
