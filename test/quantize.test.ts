import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { BudgetExceededError, parseDecimal } from '../src/index.ts'
import type { RoundingMode } from '../src/index.ts'

describe('quantize rounding modes', () => {
  const cases: Array<[string, number, RoundingMode, string]> = [
    ['2.5', 0, 'half-even', '2'],
    ['3.5', 0, 'half-even', '4'],
    ['2.5', 0, 'half-up', '3'],
    ['2.5', 0, 'half-down', '2'],
    ['2.5', 0, 'down', '2'],
    ['2.5', 0, 'up', '3'],
    ['2.5', 0, 'floor', '2'],
    ['2.5', 0, 'ceil', '3'],
    ['-2.5', 0, 'half-even', '-2'],
    ['-2.5', 0, 'half-up', '-3'],
    ['-2.5', 0, 'down', '-2'],
    ['-2.5', 0, 'up', '-3'],
    ['-2.5', 0, 'floor', '-3'],
    ['-2.5', 0, 'ceil', '-2'],
    ['-1.225', -2, 'half-even', '-1.22'], // 122 is even
    ['-1.235', -2, 'half-even', '-1.24'], // 123 is odd
    ['1.2349', -2, 'half-up', '1.23'],
    ['1.2350', -2, 'half-up', '1.24'],
    ['9.99', 0, 'half-up', '10'],
    ['0.5', 0, 'half-even', '0'], // ties to even zero
  ]
  for (const [input, targetExp, mode, expected] of cases) {
    it(`${input} @ 1e${targetExp} ${mode} -> ${expected}`, () => {
      const { value } = parseDecimal(input).quantize(targetExp, mode)
      assert.equal(value.toString({ format: 'fixed' }), expected)
    })
  }
})

describe('quantize evidence', () => {
  it('reports exact discarded digits and half relation', () => {
    const { value, evidence } = parseDecimal('1.2345').quantize(-3, 'half-even')
    assert.equal(value.toString({ format: 'fixed' }), '1.234')
    assert.deepEqual(evidence, {
      mode: 'half-even',
      targetExp: -3,
      inexact: true,
      keptDigits: '1234',
      discardedDigits: '5',
      relationToHalf: 'equal',
      incremented: false,
      direction: -1,
    })
  })

  it('marks exact quantizations as exact', () => {
    const { value, evidence } = parseDecimal('1.5').quantize(-2, 'half-up')
    assert.equal(value.toString({ format: 'fixed' }), '1.50')
    assert.equal(value.sigDigits, 3)
    assert.equal(value.trailingZeros, 1)
    assert.deepEqual(evidence, {
      mode: 'half-up',
      targetExp: -2,
      inexact: false,
      keptDigits: '15',
      discardedDigits: '',
      relationToHalf: 'zero',
      incremented: false,
      direction: 0,
    })
  })

  it('evidence direction matches the true sign of (result - original)', () => {
    const up = parseDecimal('1.231').quantize(-2, 'up')
    assert.equal(up.evidence.direction, 1)
    assert.equal(up.value.compare(parseDecimal('1.231')), 1)
    const down = parseDecimal('-1.231').quantize(-2, 'down')
    assert.equal(down.evidence.direction, 1) // -1.23 > -1.231
    assert.equal(down.value.compare(parseDecimal('-1.231')), 1)
    const floor = parseDecimal('-1.231').quantize(-2, 'floor')
    assert.equal(floor.evidence.direction, -1)
    assert.equal(floor.value.toString({ format: 'fixed' }), '-1.24')
  })

  it('keeps discarded digits verbatim, including zeros', () => {
    const { evidence } = parseDecimal('7.00001').quantize(-2, 'down')
    assert.equal(evidence.discardedDigits, '001')
    assert.equal(evidence.relationToHalf, 'less')
    assert.ok(evidence.inexact)
  })

  it('quantizes huge-exponent values without expansion when dropping digits', () => {
    const opts = { maxExponent: Number.MAX_SAFE_INTEGER }
    const d = parseDecimal('1.2345e9007199254740991', opts)
    const { value, evidence } = d.quantize(9007199254740990, 'half-even')
    assert.equal(value.coeff, 12n)
    assert.equal(value.exp, 9007199254740990)
    assert.equal(evidence.discardedDigits, '345')
    assert.equal(evidence.relationToHalf, 'less')
  })

  it('quantizing to a far coarser scale collapses to zero with sign metadata', () => {
    const { value, evidence } = parseDecimal('-0.4').quantize(0, 'down')
    assert.ok(value.isZero())
    assert.ok(value.isNegativeZero())
    assert.equal(evidence.keptDigits, '0')
    assert.equal(evidence.discardedDigits, '4')
    assert.equal(evidence.relationToHalf, 'less')
  })

  it('rejects target exponents beyond the exponent budget', () => {
    const d = parseDecimal('1.5')
    assert.throws(
      () => d.quantize(200000, 'down'),
      (err: unknown) => {
        assert.ok(err instanceof BudgetExceededError)
        assert.equal(err.budget, 'exponent')
        return true
      },
    )
  })

  it('exact upscale of a big coefficient stays exact', () => {
    const d = parseDecimal('1e100')
    const { value, evidence } = d.quantize(0, 'half-even')
    assert.equal(evidence.inexact, false)
    assert.equal(value.coeff, 10n ** 100n)
    assert.equal(value.sigDigits, 101)
    assert.equal(value.toString({ format: 'scientific' }), `1.${'0'.repeat(100)}e+100`)
  })
})
