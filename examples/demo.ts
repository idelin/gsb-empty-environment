import {
  BudgetExceededError,
  DecimalParseError,
  DecimalParser,
  IntervalViolationError,
  parseDecimal,
} from '../src/index.ts'

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`)
  fn()
}

show('strict parse + significant-digit metadata', () => {
  for (const input of ['1.2300', '1.23', '-0', '-0.00', '0.0012300', '1.23e4']) {
    const d = parseDecimal(input)
    console.log(
      `${input.padEnd(10)} sigDigits=${d.sigDigits} trailingZeros=${d.trailingZeros} ` +
        `negativeZero=${d.negativeZero} fixed=${d.toString({ format: 'fixed' })} ` +
        `sci=${d.toString({ format: 'scientific' })}`,
    )
  }
})

show('1.2300 == 1.23 numerically, but metadata differs', () => {
  const a = parseDecimal('1.2300')
  const b = parseDecimal('1.23')
  console.log(`equals=${a.equals(b)} identical=${a.identical(b)}`)
})

show('incremental chunked parsing keeps the raw lexeme', () => {
  const parser = new DecimalParser()
  for (const chunk of ['-1.', '230', '0e', '+', '5']) parser.feed(chunk)
  const d = parser.finish()
  console.log(`raw=${d.raw} fixed=${d.toString({ format: 'fixed' })} sigDigits=${d.sigDigits}`)
})

show('huge exponents: budget first, compare without expansion', () => {
  try {
    parseDecimal(`1e${'9'.repeat(500)}`)
  } catch (err) {
    if (err instanceof BudgetExceededError) {
      console.log(`budget exceeded at pos=${err.pos} budget=${err.budget} limit=${err.limit}`)
    }
  }
  const opts = { maxExponent: Number.MAX_SAFE_INTEGER }
  const a = parseDecimal('1e9007199254740991', opts)
  const b = parseDecimal('2e9007199254740991', opts)
  const t0 = performance.now()
  const cmp = a.compare(b)
  console.log(`1e(2^53-1) vs 2e(2^53-1) -> ${cmp} in ${(performance.now() - t0).toFixed(3)}ms`)
})

show('quantize with exact rounding evidence', () => {
  const { value, evidence } = parseDecimal('1.2345').quantize(-3, 'half-even')
  console.log(`1.2345 -> ${value.toString({ format: 'fixed' })}`)
  console.log(`evidence=${JSON.stringify(evidence)}`)
  const pi = parseDecimal('3.14159265').quantize(-4, 'half-up')
  console.log(`3.14159265 -> ${pi.value.toString({ format: 'fixed' })} (${JSON.stringify(pi.evidence.discardedDigits)} discarded, ${pi.evidence.relationToHalf} half)`)
})

show('interval constraints', () => {
  const min = parseDecimal('0')
  const max = parseDecimal('2')
  try {
    parseDecimal('2.0000000000000000000000001').assertInInterval({ min, max })
  } catch (err) {
    if (err instanceof IntervalViolationError) {
      console.log(`violated ${err.bound} bound: ${err.message}`)
    }
  }
  console.log(`1.5 in [0,2]: ${parseDecimal('1.5').assertInInterval({ min, max }).toString()}`)
})

show('error positions: illegal exponent / comma / truncation', () => {
  for (const input of ['1e1.5', '1,000', '1e+']) {
    try {
      parseDecimal(input)
    } catch (err) {
      if (err instanceof DecimalParseError) {
        console.log(`${JSON.stringify(input)} -> ${err.code} at pos=${err.pos}: ${err.message}`)
      }
    }
  }
})

show('no Number transit: precision beyond doubles', () => {
  const a = parseDecimal('9007199254740993')
  const b = parseDecimal('9007199254740992')
  console.log(`9007199254740993 vs 9007199254740992 -> ${a.compare(b)} (Number would tie)`)
})
