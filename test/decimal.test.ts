import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { IntervalViolationError, parseDecimal } from '../src/index.ts'

describe('numeric comparison vs significant-digit metadata', () => {
  it('1.2300 equals 1.23 numerically but keeps distinct metadata', () => {
    const a = parseDecimal('1.2300')
    const b = parseDecimal('1.23')
    assert.equal(a.compare(b), 0)
    assert.ok(a.equals(b))
    assert.ok(!a.identical(b))
    assert.equal(a.sigDigits, 5)
    assert.equal(b.sigDigits, 3)
    assert.equal(a.trailingZeros, 2)
    assert.equal(b.trailingZeros, 0)
  })

  it('negative zero equals zero numerically but is not conflated', () => {
    const negZero = parseDecimal('-0')
    const posZero = parseDecimal('0')
    const negZeroFrac = parseDecimal('-0.00')
    assert.equal(negZero.compare(posZero), 0)
    assert.ok(negZero.isNegativeZero())
    assert.ok(!posZero.isNegativeZero())
    assert.ok(!negZero.identical(posZero))
    assert.ok(!negZeroFrac.identical(negZero))
    assert.equal(negZeroFrac.trailingZeros, 2)
    assert.equal(negZero.toString(), '-0')
    assert.equal(negZeroFrac.toString({ format: 'fixed' }), '-0.00')
  })

  it('compares huge exponents without expanding them', () => {
    const opts = { maxExponent: Number.MAX_SAFE_INTEGER }
    const a = parseDecimal('1e9007199254740991', opts)
    const b = parseDecimal('2e9007199254740991', opts)
    const c = parseDecimal('1e9007199254740990', opts)
    const d = parseDecimal('10e9007199254740990', opts)
    assert.equal(a.compare(b), -1)
    assert.equal(b.compare(a), 1)
    assert.equal(a.compare(c), 1)
    assert.equal(a.compare(d), 0)
    assert.equal(a.compare(a), 0)
  })

  it('compares tiny negative exponents without expansion', () => {
    const opts = { maxExponent: Number.MAX_SAFE_INTEGER }
    const a = parseDecimal('1.5e-9007199254740991', opts)
    const b = parseDecimal('2.5e-9007199254740991', opts)
    assert.equal(a.compare(b), -1)
    assert.equal(parseDecimal('-1e-5').compare(parseDecimal('-2e-5')), 1)
  })

  it('keeps full precision beyond Number doubles', () => {
    const a = parseDecimal('9007199254740993')
    const b = parseDecimal('9007199254740992')
    assert.equal(a.compare(b), 1)
    const big1 = parseDecimal('123456789012345678901234567890.00000000000000000001')
    const big2 = parseDecimal('123456789012345678901234567890.00000000000000000002')
    assert.equal(big1.compare(big2), -1)
  })

  it('orders across signs, zeros and magnitudes', () => {
    const ordered = ['-1e5', '-2.5', '-0.001', '0', '0.001', '2.5', '1e5']
    const values = ordered.map(parseDecimal)
    for (let i = 0; i < values.length; i++) {
      for (let j = 0; j < values.length; j++) {
        const expected = i < j ? -1 : i > j ? 1 : 0
        assert.equal(values[i].compare(values[j]), expected, `${ordered[i]} vs ${ordered[j]}`)
      }
    }
    // negative zero slots in as numerically equal to zero
    assert.equal(parseDecimal('-0').compare(parseDecimal('0')), 0)
    assert.equal(parseDecimal('-0').compare(parseDecimal('0.001')), -1)
  })
})

describe('interval constraints', () => {
  it('accepts values inside the interval', () => {
    const d = parseDecimal('1.5')
    assert.equal(
      d.assertInInterval({ min: parseDecimal('1'), max: parseDecimal('2') }),
      d,
    )
  })

  it('respects inclusive and exclusive bounds', () => {
    const one = parseDecimal('1')
    assert.ok(one.assertInInterval({ min: one }))
    assert.throws(
      () => one.assertInInterval({ min: one, minInclusive: false }),
      (err: unknown) => {
        assert.ok(err instanceof IntervalViolationError)
        assert.equal(err.bound, 'min')
        assert.equal(err.inclusive, false)
        return true
      },
    )
    assert.throws(
      () => one.assertInInterval({ max: one, maxInclusive: false }),
      (err: unknown) => {
        assert.ok(err instanceof IntervalViolationError)
        assert.equal(err.bound, 'max')
        return true
      },
    )
  })

  it('rejects out-of-range values with exact decimal evidence', () => {
    const d = parseDecimal('2.0000000000000000000000001')
    assert.throws(
      () => d.assertInInterval({ min: parseDecimal('0'), max: parseDecimal('2') }),
      (err: unknown) => {
        assert.ok(err instanceof IntervalViolationError)
        assert.equal(err.bound, 'max')
        assert.equal(err.value, '2.0000000000000000000000001')
        return true
      },
    )
  })

  it('handles negative zero at the boundary', () => {
    const negZero = parseDecimal('-0')
    assert.ok(negZero.assertInInterval({ min: parseDecimal('0') }))
  })
})
