import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { IncommensurableError, QuantityTypeError } from '../src/errors.ts';
import { AbsoluteQuantity, DeltaQuantity, addQuantities } from '../src/quantity.ts';
import { Rational } from '../src/rational.ts';
import { celsius, centimeter, fahrenheit, inch, kelvin, meter, second } from '../src/units.ts';

describe('exact ratio conversions', () => {
  it('cm -> m is exactly 1/100', () => {
    const q = DeltaQuantity.of(37n, centimeter).to(meter);
    assert.equal(q.value.toString(), '37/100');
    assert.equal(q.value.toDecimalString(2, 'half-even'), '0.37');
  });

  it('big values stay exact, never routed through Number', () => {
    const big = 123456789012345678901234567890n;
    const q = DeltaQuantity.of(big, centimeter).to(meter);
    assert.equal(q.value.num, 12345678901234567890123456789n);
    assert.equal(q.value.den, 10n);
    const back = q.to(centimeter);
    assert.equal(back.value.num, big);
  });

  it('inch -> cm uses the exact 2.54 factor', () => {
    const q = DeltaQuantity.of(1n, inch).to(centimeter);
    assert.equal(q.value.toString(), '127/50');
    assert.equal(q.value.toDecimalString(2, 'half-even'), '2.54');
  });
});

describe('absolute vs delta quantities', () => {
  it('celsius DELTA ignores the offset: Δ10 °C === Δ10 K', () => {
    const d = DeltaQuantity.of(10n, celsius).to(kelvin);
    assert.equal(d.value.toString(), '10');
  });

  it('celsius ABSOLUTE applies the offset: 20 °C === 293.15 K', () => {
    const a = AbsoluteQuantity.of(20n, celsius).to(kelvin);
    assert.equal(a.value.toString(), '5863/20');
    assert.equal(a.value.toDecimalString(2, 'half-even'), '293.15');
  });

  it('absolute fahrenheit round-trips exactly through kelvin', () => {
    const a = AbsoluteQuantity.of(Rational.parse('32'), fahrenheit).to(celsius);
    assert.equal(a.value.toString(), '0');
    const b = AbsoluteQuantity.of(Rational.parse('212'), fahrenheit).to(celsius);
    assert.equal(b.value.toString(), '100');
  });

  it('fahrenheit DELTA scales by 5/9 with no offset', () => {
    const d = DeltaQuantity.of(9n, fahrenheit).to(kelvin);
    assert.equal(d.value.toString(), '5');
  });

  it('absolute - absolute = delta; absolute + delta = absolute', () => {
    const t1 = AbsoluteQuantity.of(30n, celsius);
    const t2 = AbsoluteQuantity.of(12n, celsius);
    const diff = t1.sub(t2);
    assert.ok(diff instanceof DeltaQuantity);
    assert.equal(diff.value.toString(), '18');
    const later = t2.add(diff);
    assert.ok(later instanceof AbsoluteQuantity);
    assert.equal(later.value.toString(), '30');
  });

  it('rejects absolute + absolute', () => {
    const t1 = AbsoluteQuantity.of(30n, celsius);
    const t2 = AbsoluteQuantity.of(12n, celsius);
    assert.throws(() => addQuantities(t1, t2), QuantityTypeError);
  });
});

describe('dimension safety', () => {
  it('meters cannot convert to seconds', () => {
    const length = DeltaQuantity.of(1n, meter);
    assert.throws(() => length.to(second), IncommensurableError);
    const temp = AbsoluteQuantity.of(0n, kelvin);
    assert.throws(() => temp.to(meter), IncommensurableError);
  });
});
