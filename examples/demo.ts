/**
 * 运行样例：5 阶希尔伯特矩阵的精确行列式与逆矩阵。
 * 运行：npm run demo
 */

import { hilbert } from '../src/hilbert.ts';
import { RationalMatrix } from '../src/matrix.ts';

const N = 5;
const H = hilbert(N);

console.log(`=== ${N} 阶希尔伯特矩阵 H ===`);
console.log(H.toString());

const det = H.determinant();
console.log('\n=== 精确行列式 det(H) ===');
console.log(`分子 num = ${det.num}`);
console.log(`分母 den = ${det.den}`);
console.log(`det(H) = ${det.toString()}`);
console.log(`浮点近似 = ${det.toFloat()}`);
console.log(`理论值 1/266716800000 ≈ ${1 / 266716800000}`);

const inv = H.inverse();
console.log('\n=== 精确逆矩阵 H^{-1}（5 阶 Hilbert 逆恰为整数矩阵）===');
console.log(inv.toString());

const product = H.mul(inv);
console.log('\n=== 验证 H * H^{-1} ===');
console.log(product.toString());
const identity = RationalMatrix.identity(N);
console.log(`\nH * H^{-1} === I（严格相等）: ${product.equals(identity)}`);
console.log(`H^{-1} * H === I（严格相等）: ${inv.mul(H).equals(identity)}`);

// 奇异性精确判定示例
const singular = new RationalMatrix([
  [1, 2, 3],
  [4, 5, 6],
  [7, 8, 9],
]);
console.log(`\n=== 奇异性判定 ===`);
console.log(`det([[1,2,3],[4,5,6],[7,8,9]]) = ${singular.determinant().toString()}`);
console.log(`isSingular = ${singular.isSingular()}`);
try {
  singular.inverse();
} catch (e) {
  console.log(`inverse() 抛出: ${(e as Error).name}: ${(e as Error).message}`);
}
