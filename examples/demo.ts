import {
  AbsoluteQuantity,
  ConversionGraph,
  DeltaQuantity,
  Rational,
  RoundingMode,
  addQuantities,
  units,
} from '../src/index.ts';

const { celsius, centimeter, fahrenheit, kelvin, meter, second } = units;

function show(label: string, fn: () => unknown): void {
  try {
    console.log(`${label}\n    => ${fn()}`);
  } catch (err) {
    const e = err as Error;
    console.log(`${label}\n    => [${e.name}] ${e.message}`);
  }
}

console.log('--- exact ratio conversions ---');
show('37 cm in m (exact rational)', () =>
  DeltaQuantity.of(37n, centimeter).to(meter).value.toString(),
);
show('huge BigInt cm -> m (no Number involved)', () =>
  DeltaQuantity.of(123456789012345678901234567890n, centimeter).to(meter).value.toString(),
);
show('1/3 m rendered with explicit rounding', () =>
  `${Rational.of(1n, 3n).toDecimalString(4, RoundingMode.HalfEven)} m (half-even, 4 places)`,
);

console.log('\n--- absolute vs delta ---');
show('Δ10 °C in K (offset NOT applied)', () =>
  DeltaQuantity.of(10n, celsius).to(kelvin).toString(),
);
show('absolute 20 °C in K (offset applied)', () =>
  AbsoluteQuantity.of(20n, celsius).to(kelvin).toString(),
);
show('absolute 212 °F in °C', () =>
  AbsoluteQuantity.of(212n, fahrenheit).to(celsius).toString(),
);
show('30 °C + Δ5 K', () =>
  AbsoluteQuantity.of(30n, celsius).add(DeltaQuantity.of(5n, kelvin)).toString(),
);
show('30 °C + 12 °C (absolute + absolute)', () =>
  addQuantities(AbsoluteQuantity.of(30n, celsius), AbsoluteQuantity.of(12n, celsius)).toString(),
);

console.log('\n--- dimension safety ---');
show('1 m in seconds', () => DeltaQuantity.of(1n, meter).to(second).toString());

console.log('\n--- conversion graph & contradiction detection ---');
const graph = new ConversionGraph();
graph.addConversion('cm', 'm', Rational.of(1n, 100n));
graph.addConversion('m', 'km', Rational.of(1n, 1000n));
graph.addConversion('°C', 'K', Rational.ONE, Rational.parse('273.15'));
show('cm -> km implied factor', () => graph.findConversion('cm', 'km')!.m.toString());
show('250000 cm in km (via composed path)', () =>
  graph.convertDelta(Rational.of(250000n), 'cm', 'km').toString(),
);
show('adding contradictory edge cm -> m = 1/99', () => {
  graph.addConversion('cm', 'm', Rational.of(1n, 99n));
  return 'accepted (BUG!)';
});
show('re-adding consistent edge cm -> m = 1/100', () => {
  graph.addConversion('cm', 'm', Rational.of(1n, 100n));
  return 'accepted (consistent duplicate)';
});
