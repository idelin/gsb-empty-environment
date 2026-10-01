import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  BudgetExceededError,
  DecimalParseError,
  DecimalParser,
  parseDecimal,
} from '../src/index.ts'

function chunkings(s: string): string[][] {
  const result: string[][] = []
  const n = s.length
  for (let i = 0; i <= n; i++) result.push([s.slice(0, i), s.slice(i)])
  for (let i = 0; i <= n; i++) {
    for (let j = i; j <= n; j++) {
      result.push([s.slice(0, i), s.slice(i, j), s.slice(j)])
    }
  }
  result.push([...s])
  return result
}

describe('chunked parsing equals whole-string parsing', () => {
  const samples = [
    '0',
    '-0',
    '-0.00',
    '1.2300',
    '+1.5e-3',
    '-2.5E+30',
    '0.0012300',
    '123456789012345678901234567890.00000000000000000001',
    '9e99999',
    '.5',
    '007',
  ]
  for (const sample of samples) {
    it(`all chunkings of ${JSON.stringify(sample)} agree`, () => {
      const whole = parseDecimal(sample).snapshot()
      for (const chunks of chunkings(sample)) {
        const parser = new DecimalParser()
        for (const chunk of chunks) parser.feed(chunk)
        const got = parser.finish().snapshot()
        assert.deepEqual(got, whole, `chunks ${JSON.stringify(chunks)}`)
        assert.equal(got.raw, sample, 'raw lexeme preserved across chunks')
      }
    })
  }
})

describe('error positions are independent of chunking', () => {
  const errorSamples: Array<[string, number, string]> = [
    ['1e1.5', 3, 'unexpected-char'],
    ['1,000', 1, 'unexpected-char'],
    ['12.34.5', 5, 'unexpected-char'],
    ['1e+', 3, 'truncated'],
    ['1e', 2, 'truncated'],
    ['1.', 2, 'truncated'],
    ['-', 1, 'truncated'],
  ]
  for (const [input, pos, code] of errorSamples) {
    it(`${JSON.stringify(input)} fails at pos ${pos} for every chunking`, () => {
      for (const chunks of chunkings(input)) {
        const parser = new DecimalParser()
        let err: unknown
        try {
          for (const chunk of chunks) parser.feed(chunk)
          parser.finish()
        } catch (e) {
          err = e
        }
        assert.ok(err instanceof DecimalParseError, `chunks ${JSON.stringify(chunks)}`)
        assert.equal(err.pos, pos, `chunks ${JSON.stringify(chunks)}`)
        assert.equal(err.code, code, `chunks ${JSON.stringify(chunks)}`)
      }
    })
  }

  it('budget exhaustion position is chunking-independent', () => {
    const input = '1e101'
    const expectedPos = 4
    for (const chunks of chunkings(input)) {
      const parser = new DecimalParser({ maxExponent: 100 })
      let err: unknown
      try {
        for (const chunk of chunks) parser.feed(chunk)
        parser.finish()
      } catch (e) {
        err = e
      }
      assert.ok(err instanceof BudgetExceededError, `chunks ${JSON.stringify(chunks)}`)
      assert.equal(err.pos, expectedPos, `chunks ${JSON.stringify(chunks)}`)
      assert.equal(err.budget, 'exponent')
    }
  })

  it('finish() truncation is reported at the stream end, not the chunk end', () => {
    const parser = new DecimalParser()
    parser.feed('12')
    parser.feed('3.')
    assert.throws(
      () => parser.finish(),
      (err: unknown) => {
        assert.ok(err instanceof DecimalParseError)
        assert.equal(err.pos, 4)
        assert.equal(err.code, 'truncated')
        return true
      },
    )
  })

  it('raw lexeme accumulates across arbitrary feeds', () => {
    const parser = new DecimalParser()
    parser.feed('-1.')
    parser.feed('230')
    parser.feed('0e')
    parser.feed('+')
    parser.feed('5')
    const d = parser.finish()
    assert.equal(d.raw, '-1.2300e+5')
    assert.equal(d.toString({ format: 'fixed' }), '-123000')
    assert.equal(d.sigDigits, 5)
    assert.equal(d.trailingZeros, 2)
  })
})
