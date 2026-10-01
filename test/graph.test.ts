import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { ContradictoryConversionError } from '../src/errors.ts';
import { ConversionGraph } from '../src/graph.ts';
import { Rational } from '../src/rational.ts';

function lengthGraph(): ConversionGraph {
  const g = new ConversionGraph();
  g.addConversion('cm', 'm', Rational.of(1n, 100n));
  g.addConversion('m', 'km', Rational.of(1n, 1000n));
  return g;
}

describe('conversion graph', () => {
  it('composes conversions along a path exactly', () => {
    const g = lengthGraph();
    const conv = g.findConversion('cm', 'km');
    assert.ok(conv);
    assert.equal(conv.m.toString(), '1/100000');
    assert.equal(conv.c.toString(), '0');
    assert.equal(g.convertDelta(Rational.of(250000n), 'cm', 'km').toString(), '5/2');
  });

  it('derives the reverse edge automatically', () => {
    const g = lengthGraph();
    assert.equal(g.convertDelta(Rational.of(1n), 'm', 'cm').toString(), '100');
  });

  it('rejects a contradictory edge that closes a cycle', () => {
    const g = lengthGraph();
    assert.throws(
      () => g.addConversion('cm', 'm', Rational.of(1n, 99n)),
      ContradictoryConversionError,
    );
    assert.throws(
      () => g.addConversion('km', 'cm', Rational.of(99999n)),
      ContradictoryConversionError,
    );
  });

  it('accepts a consistent duplicate edge (idempotent)', () => {
    const g = lengthGraph();
    assert.doesNotThrow(() => g.addConversion('cm', 'm', Rational.of(1n, 100n)));
    assert.doesNotThrow(() => g.addConversion('m', 'cm', Rational.of(100n)));
  });

  it('rejects a contradictory affine offset on temperature edges', () => {
    const g = new ConversionGraph();
    g.addConversion('°C', 'K', Rational.ONE, Rational.parse('273.15'));
    assert.throws(
      () => g.addConversion('°C', 'K', Rational.ONE, Rational.parse('273.16')),
      ContradictoryConversionError,
    );
    assert.doesNotThrow(() =>
      g.addConversion('K', '°C', Rational.ONE, Rational.parse('-273.15')),
    );
  });

  it('validates multi-hop cycles for consistency', () => {
    const g = new ConversionGraph();
    g.addConversion('a', 'b', Rational.of(2n));
    g.addConversion('b', 'c', Rational.of(3n));
    // a -> c is implied as *6; a direct edge must agree
    assert.doesNotThrow(() => g.addConversion('a', 'c', Rational.of(6n)));
    assert.throws(
      () => g.addConversion('c', 'a', Rational.of(1n, 5n)),
      ContradictoryConversionError,
    );
  });

  it('delta conversion through the graph drops offsets', () => {
    const g = new ConversionGraph();
    g.addConversion('°C', 'K', Rational.ONE, Rational.parse('273.15'));
    assert.equal(g.convertDelta(Rational.of(10n), '°C', 'K').toString(), '10');
    assert.equal(g.convertAbsolute(Rational.of(10n), '°C', 'K').toString(), '5663/20');
  });

  it('reports disconnected units instead of guessing', () => {
    const g = lengthGraph();
    assert.equal(g.findConversion('m', 's'), null);
    assert.throws(() => g.convertDelta(Rational.ONE, 'm', 's'), ContradictoryConversionError);
  });
});
