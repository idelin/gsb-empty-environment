import { extractContours, maskFromContours } from '../src/pixel-contours.js';
import type { Contour, Mask } from '../src/pixel-contours.js';

type TestFn = () => void;

let passed = 0;
let failed = 0;

function test(name: string, fn: TestFn): void {
  try {
    fn();
    passed++;
    console.log('ok   ' + name);
  } catch (err) {
    failed++;
    console.error('FAIL ' + name);
    console.error(err);
  }
}

function assert(cond: boolean, msg: string): void {
  if (!cond) throw new Error(msg);
}

function maskFromRows(rows: string[]): Mask {
  const height = rows.length;
  const width = rows[0].length;
  const data = new Uint8Array(width * height);
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      if (rows[y].charAt(x) === '#') data[y * width + x] = 1;
    }
  }
  return { width: width, height: height, data: data };
}

function ofOrientation(contours: Contour[], orientation: string): Contour[] {
  const out: Contour[] = [];
  for (const c of contours) {
    if (c.orientation === orientation) out.push(c);
  }
  return out;
}

/* 提取 → 重建 → 与原掩码逐像素核对，并校验层级/几何不变量 */
function checkRoundTrip(mask: Mask, label: string): Contour[] {
  const res = extractContours(mask);
  const back = maskFromContours(mask.width, mask.height, res.contours);
  for (let i = 0; i < back.length; i++) {
    const want = mask.data[i] === 0 ? 0 : 1;
    if (back[i] !== want) {
      const x = i % mask.width;
      const y = Math.floor(i / mask.width);
      throw new Error(
        label + ': 重建掩码在 (' + x + ',' + y + ') 处不一致：期望 ' +
        want + '，实际 ' + back[i]
      );
    }
  }
  for (const c of res.contours) {
    for (let i = 0; i < c.points.length; i++) {
      const a = c.points[i];
      const b = c.points[(i + 1) % c.points.length];
      const axisAligned = (a[0] === b[0]) !== (a[1] === b[1]);
      assert(axisAligned, label + ': 轮廓边必须轴对齐');
      assert(
        Number.isInteger(a[0]) && Number.isInteger(a[1]),
        label + ': 顶点必须是整数像素边坐标'
      );
    }
    const seedVal = mask.data[c.seedY * mask.width + c.seedX];
    if (c.orientation === 'outer') {
      assert(seedVal !== 0, label + ': 外边界种子必须是前景像素');
    } else {
      assert(seedVal === 0, label + ': 孔洞种子必须是背景像素');
    }
    if (c.parent === null) {
      assert(c.orientation === 'outer', label + ': 根轮廓必须是外边界');
      assert(c.depth === 0, label + ': 根轮廓深度必须为 0');
    } else {
      const p = res.contours[c.parent];
      assert(p.orientation !== c.orientation, label + ': 父子轮廓方向必须相反');
      assert(c.depth === p.depth + 1, label + ': 深度必须沿层级递增');
      assert(p.children.indexOf(c.id) !== -1, label + ': 父轮廓必须引用子轮廓');
    }
  }
  return res.contours;
}

test('空掩码无轮廓', function () {
  const res = extractContours({ width: 3, height: 2, data: new Uint8Array(6) });
  assert(res.contours.length === 0, '应无轮廓');
});

test('单像素：单位正方形外边界', function () {
  const contours = checkRoundTrip(maskFromRows(['#']), '单像素');
  assert(contours.length === 1, '应只有 1 个轮廓');
  const c = contours[0];
  assert(c.orientation === 'outer', '应为外边界');
  assert(c.points.length === 4, '正方形应有 4 个拐角');
  assert(c.twiceArea === -2, '面积应为 1（twiceArea = -2）');
  assert(c.parent === null && c.depth === 0, '应为根轮廓');
});

test('对角接触：前景四连通 → 两个独立轮廓', function () {
  const contours = checkRoundTrip(maskFromRows(['#.', '.#']), '对角');
  assert(contours.length === 2, '对角像素不得合并为一个轮廓');
  for (const c of contours) {
    assert(c.orientation === 'outer', '都应是外边界');
    assert(c.points.length === 4, '每个都是独立单位正方形');
  }
});

test('正交相邻：合并为单个轮廓', function () {
  const contours = checkRoundTrip(maskFromRows(['##']), '相邻');
  assert(contours.length === 1, '应合并为一个轮廓');
  assert(Math.abs(contours[0].twiceArea) === 4, '面积应为 2');
});

test('3x3 环：外边界 + 孔洞层级', function () {
  const contours = checkRoundTrip(maskFromRows(['###', '#.#', '###']), '环');
  assert(contours.length === 2, '应为外边界 + 孔洞');
  const outers = ofOrientation(contours, 'outer');
  const holes = ofOrientation(contours, 'hole');
  assert(outers.length === 1 && holes.length === 1, '外边界与孔洞各一');
  const outer = outers[0];
  const hole = holes[0];
  assert(hole.parent === outer.id, '孔洞的父轮廓应是外边界');
  assert(hole.depth === 1, '孔洞深度应为 1');
  assert(outer.children.indexOf(hole.id) !== -1, '外边界应引用孔洞');
  assert(Math.abs(hole.twiceArea) === 2, '孔洞面积应为 1');
});

test('对角缺口：背景八连通 → 孔洞消失', function () {
  const contours = checkRoundTrip(maskFromRows(['.##', '#.#', '###']), '对角缺口');
  assert(ofOrientation(contours, 'hole').length === 0, '对角泄漏不得产生孔洞');
  assert(ofOrientation(contours, 'outer').length === 1, '应为单个外边界');
});

test('一像素宽通道：孔与外部四连通', function () {
  const contours = checkRoundTrip(maskFromRows(['###', '..#', '###']), '通道');
  assert(ofOrientation(contours, 'hole').length === 0, '通道使孔洞消失');
  assert(ofOrientation(contours, 'outer').length === 1, '应为单个外边界');
});

test('一像素宽蛇形通道：背景沟槽不算孔洞', function () {
  const contours = checkRoundTrip(
    maskFromRows(['#####', '....#', '#####', '#....', '#####']),
    '蛇形通道'
  );
  assert(ofOrientation(contours, 'hole').length === 0, '沟槽与外部连通');
  assert(ofOrientation(contours, 'outer').length === 1, '应为单个外边界');
});

test('一像素宽 U 形凹槽', function () {
  const contours = checkRoundTrip(maskFromRows(['#.#', '#.#', '###']), 'U形');
  assert(contours.length === 1, '应为单个外边界');
});

test('嵌套：孔洞中的前景岛', function () {
  const contours = checkRoundTrip(
    maskFromRows(['#####', '#...#', '#.#.#', '#...#', '#####']),
    '嵌套'
  );
  assert(contours.length === 3, '应为外边界 + 孔洞 + 岛');
  const byDepth: Contour[] = [];
  for (const c of contours) byDepth[c.depth] = c;
  const outer = byDepth[0];
  const hole = byDepth[1];
  const island = byDepth[2];
  assert(outer.orientation === 'outer', '第 0 层为外边界');
  assert(hole.orientation === 'hole', '第 1 层为孔洞');
  assert(island.orientation === 'outer', '第 2 层为岛');
  assert(hole.parent === outer.id, '孔洞的父是外边界');
  assert(island.parent === hole.id, '岛的父是孔洞');
});

test('满掩码：整个矩形', function () {
  const contours = checkRoundTrip(maskFromRows(['###', '###']), '满掩码');
  assert(contours.length === 1, '应为单个轮廓');
  assert(contours[0].points.length === 4, '矩形 4 个拐角');
  assert(Math.abs(contours[0].twiceArea) === 12, '面积应为 6');
});

test('一像素宽横线与竖线', function () {
  checkRoundTrip(maskFromRows(['#####']), '横线');
  checkRoundTrip(maskFromRows(['#', '#', '#', '#']), '竖线');
});

test('棋盘格：所有前景像素对角接触', function () {
  const rows: string[] = [];
  for (let y = 0; y < 4; y++) {
    let row = '';
    for (let x = 0; x < 4; x++) row += (x + y) % 2 === 0 ? '#' : '.';
    rows.push(row);
  }
  const contours = checkRoundTrip(maskFromRows(rows), '棋盘');
  assert(contours.length === 8, '8 个前景像素应各自独立');
  for (const c of contours) {
    assert(c.orientation === 'outer' && c.points.length === 4, '每个都是单位正方形');
  }
});

test('3x3 全部 512 种掩码重建一致', function () {
  for (let bits = 0; bits < 512; bits++) {
    const data = new Uint8Array(9);
    for (let i = 0; i < 9; i++) data[i] = (bits >> i) & 1;
    checkRoundTrip({ width: 3, height: 3, data: data }, '3x3#' + bits);
  }
});

test('4x4 全部 65536 种掩码重建一致', function () {
  for (let bits = 0; bits < 65536; bits++) {
    const data = new Uint8Array(16);
    for (let i = 0; i < 16; i++) data[i] = (bits >> i) & 1;
    checkRoundTrip({ width: 4, height: 4, data: data }, '4x4#' + bits);
  }
});

function mulberry32(seed: number): () => number {
  let s = seed >>> 0;
  return function (): number {
    s = (s + 0x6D2B79F5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t = (t ^ (t + Math.imul(t ^ (t >>> 7), t | 61))) >>> 0;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('随机掩码重建一致（多种尺寸与密度）', function () {
  const sizes: number[][] = [[1, 1], [2, 3], [5, 4], [8, 8], [16, 16], [31, 7], [64, 64]];
  const densities: number[] = [0.1, 0.3, 0.5, 0.7, 0.9];
  const rand = mulberry32(20261005);
  for (const sz of sizes) {
    for (const density of densities) {
      for (let iter = 0; iter < 20; iter++) {
        const w = sz[0];
        const h = sz[1];
        const data = new Uint8Array(w * h);
        for (let i = 0; i < data.length; i++) data[i] = rand() < density ? 1 : 0;
        checkRoundTrip(
          { width: w, height: h, data: data },
          '随机 ' + w + 'x' + h + ' d=' + density + ' #' + iter
        );
      }
    }
  }
});

console.log('---');
if (failed > 0) {
  console.error(failed + ' 个测试失败（共 ' + (passed + failed) + ' 个）');
  throw new Error('测试失败');
}
console.log('全部通过：' + passed + ' 个测试');
