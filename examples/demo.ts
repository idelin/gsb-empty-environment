import { separate, verifySeparation, verifyCommonPoint } from '../src/index.ts';
import type { InputPoint, SeparationResult } from '../src/index.ts';
import { Fraction } from '../src/index.ts';

function show(title: string, P: InputPoint[], Q: InputPoint[]): void {
  console.log(`=== ${title} ===`);
  console.log(`P = [${P.map((p) => `(${p.join(', ')})`).join(', ')}]`);
  console.log(`Q = [${Q.map((q) => `(${q.join(', ')})`).join(', ')}]`);
  const result: SeparationResult = separate(P, Q);
  if (result.separable) {
    const a = result.normal.map((c: Fraction) => c.toString()).join(', ');
    console.log(`判定: 严格可分`);
    console.log(`法向量 a = (${a}), 偏移 b = ${result.offset}`);
    console.log(`分离超平面: ${result.hyperplane}`);
    console.log(`严格性核验: ${verifySeparation(P, Q, result.normal, result.offset) ? '通过' : '失败'}`);
  } else {
    const pt = result.point.map((c: Fraction) => c.toString()).join(', ');
    console.log(`判定: 不可分（凸包相交）`);
    console.log(`公共点 x* = (${pt})`);
    console.log(`P 侧重心坐标 λ = [${result.coeffsP.map((c: Fraction) => c.toString()).join(', ')}]`);
    console.log(`Q 侧重心坐标 μ = [${result.coeffsQ.map((c: Fraction) => c.toString()).join(', ')}]`);
    console.log(
      `成员核验: ${verifyCommonPoint(P, Q, result.point, result.coeffsP, result.coeffsQ) ? '通过' : '失败'}`,
    );
  }
  console.log();
}

// 样例 1：二维严格可分（验收边界）
show(
  '样例 1：二维可分点集',
  [
    [0, 0],
    [1, 0],
    [0, 1],
  ],
  [
    [3, 3],
    [4, 2],
    [2, 4],
  ],
);

// 样例 2：二维凸包相交（验收边界：输出重叠区域内的公共点）
show(
  '样例 2：二维相交点集',
  [
    [0, 0],
    [4, 0],
    [0, 4],
  ],
  [
    [3, 1],
    [1, 3],
    [3, 3],
  ],
);

// 样例 3：有理数坐标，边界相触（不严格可分）
show(
  '样例 3：边界相触（共享点 (1, 0)）',
  [
    [0, 0],
    [1, 0],
  ],
  [
    [1, 0],
    [2, 0],
  ],
);

// 样例 4：三维可分
show(
  '样例 4：三维可分点集',
  [
    [0, 0, 0],
    [1, 0, 0],
    [0, 1, 0],
  ],
  [
    [0, 0, 3],
    [1, 0, 4],
    [0, 1, 3],
  ],
);

// 样例 5：有理数坐标相交
show(
  '样例 5：有理数坐标相交',
  [
    ['1/2', '1/3'],
    ['5/2', '1/3'],
    ['1/2', '7/3'],
  ],
  [
    [1, 1],
    [2, 1],
    [1, 2],
  ],
);
