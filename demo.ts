import { rootN, verifyRoundedRoot, formatDecimal, parseDecimal } from './src/index.ts';

function show(label: string, input: string, n: number, scale: number, mode?: 'floor' | 'ceil' | 'half-up' | 'half-down' | 'half-even') {
  const res = rootN(input, n, scale, mode ? { mode } : {});
  const ok = verifyRoundedRoot(input, n, scale, res);
  console.log(`${label}`);
  console.log(`  input      = ${input}`);
  console.log(`  result     = ${formatDecimal(res)}   (mode=${res.mode}, exact=${res.exact}, verified=${ok})`);
  console.log(`  bracket    = ${res.floor} <= root*10^${scale} <= ${res.ceil}`);
  console.log(`  true root in [${formatDecimal(res.lower)}, ${formatDecimal(res.upper)}], maxError=${formatDecimal(res.maxError)}`);
  console.log(`  remainder  = ${res.scaledRemainder}  (radicand - floor^${n}, integer-root level)`);
  console.log(`  iterations = ${res.iterations}`);
  console.log();
}

console.log('=== BigInt fixed-point nth root: correctly rounded, exact arithmetic ===\n');

show('sqrt(2) to 50 fractional digits', '2', 2, 50);
show('cbrt(2) to 30 fractional digits', '2', 3, 30);
show('10th root of 1024 (perfect power)', '1024', 10, 3);
show('sqrt of perfect square 12345^2', '152399025', 2, 4);

console.log('--- midpoint ties, decided by exact integer-power comparison ---');
show('sqrt(6.25) = 2.5 exactly, half-even -> 2', '6.25', 2, 0);
show('sqrt(30.25) = 5.5 exactly, half-even -> 6', '30.25', 2, 0);
show('sqrt(6.25) with half-up -> 3', '6.25', 2, 0, 'half-up');
show('cbrt(3.375) = 1.5 exactly, half-even -> 2', '3.375', 3, 0);

console.log('--- tiny inputs are never truncated to zero ---');
show('sqrt(9e-11) at scale 10', '0.00000000009', 2, 10);
show('sqrt(1e-1000) = 1e-500 exactly, scale 600', '1e-1000', 2, 600);

console.log('--- huge operands, input/output precision independent ---');
show('sqrt(10^120 + 7) at scale 20', '1' + '0'.repeat(119) + '7', 2, 20);

console.log('--- negative input is rejected ---');
try {
  rootN('-4', 2, 2);
  console.log('ERROR: negative input was accepted');
} catch (err) {
  console.log(`  rootN("-4", 2, 2) threw ${(err as Error).name}: ${(err as Error).message}\n`);
}

console.log('--- digit budget is enforced ---');
try {
  rootN('2', 2, 5000, { maxDigits: 1000 });
  console.log('ERROR: budget exceeded but no error thrown');
} catch (err) {
  console.log(`  rootN("2", 2, 5000, {maxDigits: 1000}) threw ${(err as Error).name}: ${(err as Error).message}\n`);
}

console.log('--- recovery: format -> parse keeps the same rounded result ---');
const original = parseDecimal('123456789123456789.000125');
const recovered = parseDecimal(formatDecimal(original));
const a = rootN(original, 2, 15);
const b = rootN(recovered, 2, 15);
console.log(`  original  = ${formatDecimal(original)}`);
console.log(`  recovered = ${formatDecimal(recovered)}`);
console.log(`  sqrt scale 15: ${formatDecimal(a)} (same after recovery: ${a.coeff === b.coeff})`);
