import { maskToString, Point, rasterize } from '../src/index';

function show(title: string, poly: Point[], width: number, height: number): void {
  const r = rasterize(poly, { width, height });
  console.log(`=== ${title} ===`);
  console.log('even-odd:');
  console.log(maskToString(r.evenOdd, width, height));
  console.log('non-zero:');
  console.log(maskToString(r.nonZero, width, height));
  console.log('diff (两规则判定不同的区域):');
  console.log(maskToString(r.diff, width, height));
  console.log();
}

// 同一正方形绕两圈：内部绕数为 2，两规则结论相反。
const doubleWound: Point[] = [
  { x: 2, y: 2 }, { x: 10, y: 2 }, { x: 10, y: 10 }, { x: 2, y: 10 },
  { x: 2, y: 2 }, { x: 10, y: 2 }, { x: 10, y: 10 }, { x: 2, y: 10 },
];
show('double-wound square', doubleWound, 13, 13);

// 同向嵌套正方形：内环绕数为 2，even-odd 镂空、non-zero 填充。
const nested: Point[] = [
  { x: 0, y: 0 }, { x: 12, y: 0 }, { x: 12, y: 12 }, { x: 0, y: 12 }, { x: 0, y: 0 },
  { x: 4, y: 4 }, { x: 8, y: 4 }, { x: 8, y: 8 }, { x: 4, y: 8 }, { x: 4, y: 4 },
];
show('nested squares (same orientation)', nested, 13, 13);

// 自交领结：两瓣绕数 +1/-1，两种规则一致，diff 为空。
const bowtie: Point[] = [{ x: 1, y: 1 }, { x: 11, y: 11 }, { x: 11, y: 1 }, { x: 1, y: 11 }];
show('self-intersecting bowtie', bowtie, 13, 13);
