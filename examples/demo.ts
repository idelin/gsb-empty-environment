/**
 * Runnable tour of the exact-comparison protocol.
 * Run: node --experimental-strip-types examples/demo.ts   (Node >= 22.6)
 */
import {
  CompareSession,
  compareAffine,
  describeSession,
  digitStreamProvider,
  exactProvider,
  exactValue,
  fixedIntervalProvider,
  halvingProvider,
  parseRational,
  parseValue,
  toDecimalString,
  type CompareResult,
} from '../src/index.ts';

function show(title: string, fn: () => void): void {
  console.log(`\n=== ${title} ===`);
  fn();
}

function print(r: CompareResult): void {
  console.log(r.kind === 'decided' ? `DECIDED: ${r.outcome}` : `UNDETERMINED: ${r.reason}`);
}

show('1. Big integers differing by 1 (no Number involved)', () => {
  const a = exactValue(parseRational('1180591620717411303424')); // 2^70
  const b = exactValue(parseRational('1180591620717411303425')); // 2^70 + 1
  print(compareAffine(a, b));
  console.log('(as JS Numbers they are identical:', Number(2n ** 70n) === Number(2n ** 70n + 1n), ')');
});

show('2. Rational vs long decimal, exact', () => {
  print(compareAffine(exactValue(parseRational('1/3')), exactValue(parseRational('0.333333333333333333'))));
});

show('3. Overlapping intervals are NOT "approximately equal"', () => {
  print(compareAffine(parseValue('1.5±0.1', 'sensor-A'), parseValue('1.5±0.1', 'sensor-B')));
});

show('4. Shared provenance: same instrument error cancels', () => {
  print(compareAffine(parseValue('1.5±0.1', 'sensor-7'), parseValue('1.5±0.1', 'sensor-7')));
  print(compareAffine(parseValue('1.0±0.1', 'sensor-7'), parseValue('2.0±0.1', 'sensor-7')));
});

show('5. Refinement protocol with a budget, then suspend/resume', () => {
  const mkL = () => halvingProvider('L', parseRational('1.000'), 'src-L', parseRational('0.01'));
  const mkR = () => halvingProvider('R', parseRational('1.001'), 'src-R', parseRational('0.01'));

  const session = new CompareSession(mkL(), mkR(), 2);
  print(session.run());
  console.log('state:', describeSession(session));

  const saved = session.suspend();
  console.log('suspended:', saved);

  const resumed = CompareSession.resume(saved, mkL(), mkR(), 50);
  print(resumed.run());
  console.log('state:', describeSession(resumed));
});

show('6. Digit-stream evidence: 22/7 vs pi', () => {
  const pi = digitStreamProvider('pi', '3.141592653589793238462643383279', 'pi', 2);
  const approx = exactProvider('22/7', parseRational('22/7'));
  const s = new CompareSession(approx, pi);
  print(s.run());
  console.log('state:', describeSession(s));
});

show('7. Terminal pending: non-refinable overlap stays undetermined', () => {
  const s = new CompareSession(
    fixedIntervalProvider('a', parseRational('1'), 'A', parseRational('0.5')),
    fixedIntervalProvider('b', parseRational('1'), 'B', parseRational('0.5')),
  );
  print(s.run());
});

show('8. Explicit rounding when a decimal printout is needed', () => {
  const third = parseRational('1/3');
  console.log('1/3 @6dp floor     =', toDecimalString(third, 6, 'floor'));
  console.log('1/3 @6dp ceil      =', toDecimalString(third, 6, 'ceil'));
  console.log('1/3 @6dp half-even =', toDecimalString(third, 6, 'half-even'));
});

show('9. NaN is rejected as unsupported', () => {
  try {
    parseRational('NaN');
  } catch (e) {
    console.log('rejected:', (e as Error).message);
  }
});
