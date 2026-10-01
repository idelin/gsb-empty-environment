import { dims } from './dimension.ts';
import { Rational } from './rational.ts';
import { Unit } from './unit.ts';

const L = dims({ L: 1 });
const M = dims({ M: 1 });
const T = dims({ T: 1 });
const THETA = dims({ Θ: 1 });

// --- length (canonical base: meter) ---
export const meter = new Unit('meter', 'm', L, Rational.ONE);
export const centimeter = new Unit('centimeter', 'cm', L, Rational.of(1n, 100n));
export const millimeter = new Unit('millimeter', 'mm', L, Rational.of(1n, 1000n));
export const kilometer = new Unit('kilometer', 'km', L, Rational.of(1000n));
export const inch = new Unit('inch', 'in', L, Rational.of(254n, 10000n)); // exact: 1 in = 0.0254 m

// --- time (canonical base: second) ---
export const second = new Unit('second', 's', T, Rational.ONE);
export const minute = new Unit('minute', 'min', T, Rational.of(60n));
export const hour = new Unit('hour', 'h', T, Rational.of(3600n));

// --- mass (canonical base: kilogram) ---
export const kilogram = new Unit('kilogram', 'kg', M, Rational.ONE);
export const gram = new Unit('gram', 'g', M, Rational.of(1n, 1000n));

// --- absolute temperature (canonical base: kelvin) ---
export const kelvin = new Unit('kelvin', 'K', THETA, Rational.ONE);
export const celsius = new Unit('celsius', '°C', THETA, Rational.ONE, Rational.parse('273.15'));
// K = °F * 5/9 + 459.67 * 5/9  =>  offset = 45967/180 exactly
export const fahrenheit = new Unit('fahrenheit', '°F', THETA, Rational.of(5n, 9n), Rational.of(45967n, 180n));

export const allUnits: readonly Unit[] = [
  meter, centimeter, millimeter, kilometer, inch,
  second, minute, hour,
  kilogram, gram,
  kelvin, celsius, fahrenheit,
];
