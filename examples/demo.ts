/**
 * Demo: exact determinant and inverse of the 5x5 Hilbert matrix,
 * a famously ill-conditioned matrix where float64 fails badly.
 */
import { hilbert, Matrix } from '../src/matrix.ts';

const h5 = hilbert(5);

console.log('H5 =');
console.log(h5.toString());

const det = h5.determinant();
console.log('\nExact determinant:');
console.log(`  numerator   = ${det.num}`);
console.log(`  denominator = ${det.den}`);
console.log(`  det(H5)     = ${det}`);

// Contrast with float64, which cannot even represent the entries exactly.
const n = 5;
const hf = Array.from({ length: n }, (_, i) =>
  Array.from({ length: n }, (_, j) => 1 / (i + j + 1)),
);
let floatDet = 0;
for (let p = 0; p < n; p++) floatDet += hf[0][p] * (p % 2 ? -1 : 1) * cofactor(hf, 0, p);
function cofactor(m: number[][], rs: number, cs: number): number {
  const sub = m.filter((_, i) => i !== rs).map((row) => row.filter((_, j) => j !== cs));
  if (sub.length === 1) return sub[0][0];
  let d = 0;
  for (let p = 0; p < sub.length; p++)
    d += sub[0][p] * (p % 2 ? -1 : 1) * cofactor(sub, 0, p);
  return d;
}
console.log(`  float64 det ~= ${floatDet}  (relative error ${Math.abs(floatDet - 1 / 266716800000) / (1 / 266716800000)})`);

const inv = h5.inverse();
console.log('\nExact inverse H5^-1 (all integers!):');
console.log(inv.toString());

const product = h5.multiply(inv);
console.log('\nH5 * H5^-1 === I exactly?', product.equals(Matrix.identity(5)));
console.log(product.toString());
