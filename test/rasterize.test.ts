import { classifySample, maskToString, Point, rasterize } from '../src/index';

let failures = 0;

function expectTrue(name: string, cond: boolean, detail = ''): void {
  if (cond) {
    console.log(`ok   ${name}`);
  } else {
    failures++;
    console.log(`FAIL ${name}${detail ? ` — ${detail}` : ''}`);
  }
}

function expectMask(name: string, actual: Uint8Array, width: number, height: number, expected: readonly string[]): void {
  const got = maskToString(actual, width, height).split('\n');
  const ok = got.length === expected.length && got.every((row, i) => row === expected[i]);
  if (ok) {
    console.log(`ok   ${name}`);
    return;
  }
  failures++;
  console.log(`FAIL ${name}`);
  console.log('  expected:');
  for (const row of expected) console.log(`    ${row}`);
  console.log('  actual:');
  for (const row of got) console.log(`    ${row}`);
}

function count(mask: Uint8Array): number {
  let n = 0;
  for (const v of mask) n += v;
  return n;
}

// ---- 1. 简单正方形：两种规则一致，diff 为空 --------------------------------
{
  const square: Point[] = [{ x: 1, y: 1 }, { x: 5, y: 1 }, { x: 5, y: 5 }, { x: 1, y: 5 }];
  const r = rasterize(square, { width: 7, height: 7 });
  const expected = [
    '.......',
    '.####..',
    '.####..',
    '.####..',
    '.####..',
    '.......',
    '.......',
  ];
  expectMask('square even-odd', r.evenOdd, 7, 7, expected);
  expectMask('square non-zero', r.nonZero, 7, 7, expected);
  expectTrue('square diff empty', count(r.diff) === 0);
}

// ---- 2. 同一正方形沿同一方向绕两圈：内部绕数为 ±2 --------------------------
// even-odd 判定为外部，non-zero 判定为内部，diff 即整个内部区域。
{
  const twice: Point[] = [
    { x: 1, y: 1 }, { x: 5, y: 1 }, { x: 5, y: 5 }, { x: 1, y: 5 },
    { x: 1, y: 1 }, { x: 5, y: 1 }, { x: 5, y: 5 }, { x: 1, y: 5 },
  ];
  const r = rasterize(twice, { width: 7, height: 7 });
  const interior = [
    '.......',
    '.####..',
    '.####..',
    '.####..',
    '.####..',
    '.......',
    '.......',
  ];
  const empty = Array.from({ length: 7 }, () => '.......');
  expectMask('double-wound even-odd empty', r.evenOdd, 7, 7, empty);
  expectMask('double-wound non-zero filled', r.nonZero, 7, 7, interior);
  expectMask('double-wound diff = interior', r.diff, 7, 7, interior);
}

// ---- 3. 单条折线画出同向嵌套正方形（外 0..6，内 2..4） ----------------------
// 连接边 (0,0)->(2,2) 与闭合边 (2,2)->(0,0) 互为反向，互相抵消。
// 内区域绕数 ±2：non-zero 填充，even-odd 镂空。
{
  const nested: Point[] = [
    { x: 0, y: 0 }, { x: 6, y: 0 }, { x: 6, y: 6 }, { x: 0, y: 6 }, { x: 0, y: 0 },
    { x: 2, y: 2 }, { x: 4, y: 2 }, { x: 4, y: 4 }, { x: 2, y: 4 }, { x: 2, y: 2 },
  ];
  const r = rasterize(nested, { width: 7, height: 7 });
  expectMask('nested non-zero fills hole', r.nonZero, 7, 7, [
    '######.',
    '######.',
    '######.',
    '######.',
    '######.',
    '######.',
    '.......',
  ]);
  expectMask('nested even-odd punches hole', r.evenOdd, 7, 7, [
    '######.',
    '######.',
    '##..##.',
    '##..##.',
    '######.',
    '######.',
    '.......',
  ]);
  expectMask('nested diff = inner square', r.diff, 7, 7, [
    '.......',
    '.......',
    '..##...',
    '..##...',
    '.......',
    '.......',
    '.......',
  ]);
}

// ---- 4. 边界规则：斜边恰好穿过像素中心 --------------------------------------
// 三角形 (0,0)-(4,0)-(0,4)，斜边 x+y=4 经过中心 (i+0.5, j+0.5) 且 i+j=3 的像素，
// 按显式边界规则判定为内部。
{
  const tri: Point[] = [{ x: 0, y: 0 }, { x: 4, y: 0 }, { x: 0, y: 4 }];
  const r = rasterize(tri, { width: 5, height: 5 });
  const expected = [
    '####.',
    '###..',
    '##...',
    '#....',
    '.....',
  ];
  expectMask('triangle even-odd (boundary inside)', r.evenOdd, 5, 5, expected);
  expectMask('triangle non-zero (boundary inside)', r.nonZero, 5, 5, expected);
  expectTrue('triangle diff empty', count(r.diff) === 0);
  const onEdge = classifySample(tri, 0.5, 3.5);
  expectTrue('hypotenuse sample flagged onBoundary', onEdge.onBoundary && onEdge.insideEvenOdd && onEdge.insideNonZero);
  const outside = classifySample(tri, 3.5, 3.5);
  expectTrue('strictly outside sample', !outside.onBoundary && !outside.insideEvenOdd && !outside.insideNonZero && outside.crossingCount === 0);
  const inside = classifySample(tri, 0.5, 0.5);
  expectTrue('strictly inside sample', !inside.onBoundary && inside.crossingCount === 1 && Math.abs(inside.windingNumber) === 1);
}

// ---- 5. 菱形：多条边精确穿过像素中心，验证有理交点判定的精确性 --------------
{
  const diamond: Point[] = [{ x: 2, y: 0 }, { x: 4, y: 2 }, { x: 2, y: 4 }, { x: 0, y: 2 }];
  const r = rasterize(diamond, { width: 5, height: 5 });
  const expected = [
    '.##..',
    '####.',
    '####.',
    '.##..',
    '.....',
  ];
  expectMask('diamond even-odd', r.evenOdd, 5, 5, expected);
  expectMask('diamond non-zero', r.nonZero, 5, 5, expected);
  expectTrue('diamond diff empty', count(r.diff) === 0);
}

// ---- 6. 自交领结：两瓣绕数分别为 +1/-1，两种规则均填充，diff 为空 -----------
{
  const bowtie: Point[] = [{ x: 0, y: 0 }, { x: 4, y: 4 }, { x: 4, y: 0 }, { x: 0, y: 4 }];
  const r = rasterize(bowtie, { width: 5, height: 5 });
  const expected = [
    '#..#.',
    '####.',
    '####.',
    '#..#.',
    '.....',
  ];
  expectMask('bowtie even-odd', r.evenOdd, 5, 5, expected);
  expectMask('bowtie non-zero', r.nonZero, 5, 5, expected);
  expectTrue('bowtie diff empty', count(r.diff) === 0);
}

// ---- 7. BigInt 兜底路径：超大坐标下结果仍然精确 ------------------------------
{
  const K = 2 ** 30;
  const big: Point[] = [{ x: K, y: K }, { x: K + 4, y: K }, { x: K + 4, y: K + 4 }, { x: K, y: K + 4 }];
  const inside = classifySample(big, K + 2.5, K + 2.5);
  expectTrue('bigint: interior inside both rules', inside.insideEvenOdd && inside.insideNonZero && inside.crossingCount === 1);
  const corner = classifySample(big, K + 0.5, K + 0.5);
  expectTrue('bigint: near-corner inside', corner.insideEvenOdd && corner.insideNonZero);
  const outside = classifySample(big, K + 10.5, K + 2.5);
  expectTrue('bigint: far outside', !outside.insideEvenOdd && !outside.insideNonZero && outside.crossingCount === 0);
}

// ---- 8. 退化输入 ------------------------------------------------------------
{
  const empty = rasterize([], { width: 3, height: 3 });
  expectTrue('no vertices -> all zero', count(empty.evenOdd) === 0 && count(empty.nonZero) === 0 && count(empty.diff) === 0);

  // 两个顶点的退化"多边形"：边往返各一次互相抵消，但落在线段上的采样点按边界规则为内部。
  const seg = rasterize([{ x: 0, y: 0 }, { x: 2, y: 2 }], { width: 3, height: 3 });
  const expected = ['#..', '.#.', '...'];
  expectMask('degenerate segment: boundary pixels inside', seg.evenOdd, 3, 3, expected);
  expectMask('degenerate segment non-zero', seg.nonZero, 3, 3, expected);

  // 重复点（零长度边）不影响结果。
  const dup: Point[] = [{ x: 1, y: 1 }, { x: 5, y: 1 }, { x: 5, y: 1 }, { x: 5, y: 5 }, { x: 1, y: 5 }, { x: 1, y: 1 }];
  const r = rasterize(dup, { width: 7, height: 7 });
  expectMask('duplicate vertices ignored', r.evenOdd, 7, 7, [
    '.......',
    '.####..',
    '.####..',
    '.####..',
    '.####..',
    '.......',
    '.......',
  ]);
}

// ---- 9. 随机一致性：rasterize 逐像素结果与 classifySample 完全一致 ----------
{
  let seed = 12345;
  const rand = (): number => {
    seed = (seed * 1103515245 + 12345) & 0x7fffffff;
    return seed / 0x80000000;
  };
  let consistent = true;
  for (let t = 0; t < 25 && consistent; t++) {
    const n = 3 + Math.floor(rand() * 8);
    const poly: Point[] = [];
    for (let i = 0; i < n; i++) {
      poly.push({ x: Math.floor(rand() * 12), y: Math.floor(rand() * 12) });
    }
    const r = rasterize(poly, { width: 12, height: 12 });
    for (let j = 0; j < 12 && consistent; j++) {
      for (let i = 0; i < 12 && consistent; i++) {
        const c = classifySample(poly, i + 0.5, j + 0.5);
        const eo = c.onBoundary || c.crossingCount % 2 === 1 ? 1 : 0;
        const nz = c.onBoundary || c.windingNumber !== 0 ? 1 : 0;
        const idx = j * 12 + i;
        if (r.evenOdd[idx] !== eo || r.nonZero[idx] !== nz || r.diff[idx] !== (eo ^ nz ? 1 : 0)) {
          consistent = false;
        }
      }
    }
  }
  expectTrue('rasterize consistent with classifySample on random polygons', consistent);
}

if (failures > 0) {
  throw new Error(`${failures} test(s) failed`);
}
console.log('all tests passed');
