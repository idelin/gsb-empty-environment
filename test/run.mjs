import assert from 'node:assert/strict';
import {
  extractContours,
  traceContours,
  buildHierarchy,
  rasterize,
  masksEqual,
} from '../dist/index.js';

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

/** 由 ASCII 图构造掩码：'#'/‘1’ 为前景。 */
function maskFromArt(rows) {
  const height = rows.length;
  const width = rows[0].length;
  const data = new Uint8Array(width * height);
  rows.forEach((row, y) => {
    assert.equal(row.length, width, '行宽不一致');
    for (let x = 0; x < width; x++) {
      if (row[x] === '#' || row[x] === '1') data[y * width + x] = 1;
    }
  });
  return { width, height, data };
}

/** 掩码 -> ASCII，便于失败时查看。 */
function art(mask) {
  const lines = [];
  for (let y = 0; y < mask.height; y++) {
    let line = '';
    for (let x = 0; x < mask.width; x++) line += mask.data[y * mask.width + x] ? '#' : '.';
    lines.push(line);
  }
  return lines.join('\n');
}

/** 通用不变量：顶点为整数格点、相邻顶点为单位轴对齐边、闭合。 */
function checkContourInvariants(contours) {
  for (const c of contours) {
    assert.ok(c.vertices.length >= 4, '轮廓至少 4 个顶点');
    const n = c.vertices.length;
    for (let i = 0; i < n; i++) {
      const a = c.vertices[i];
      const b = c.vertices[(i + 1) % n];
      assert.ok(Number.isInteger(a.x) && Number.isInteger(a.y), '顶点必须是整数格点');
      const dx = Math.abs(a.x - b.x);
      const dy = Math.abs(a.y - b.y);
      assert.ok((dx === 1 && dy === 0) || (dx === 0 && dy === 1), '边必须是单位轴对齐边');
    }
    assert.equal(c.isHole, c.signedArea2 > 0, 'isHole 与有向面积符号一致');
    assert.notEqual(c.signedArea2, 0, '有向面积不为 0（无自交 8 字环）');
  }
}

/** 提取 + 重建核对，返回提取结果。 */
function roundTrip(mask) {
  const result = extractContours(mask);
  checkContourInvariants(result.contours);
  const rebuilt = rasterize(result.contours, mask.width, mask.height);
  assert.ok(
    masksEqual(rebuilt, mask),
    `重建掩码与原掩码不一致\n原图:\n${art(mask)}\n重建:\n${art(rebuilt)}`,
  );
  return result;
}

/** 前景四连通分量数 / 背景八连通分量数（不含贴边）。 */
function countComponents(mask) {
  const { width, height, data } = mask;
  const at = (x, y) => (x < 0 || y < 0 || x >= width || y >= height ? 0 : data[y * width + x]);
  const visited = new Uint8Array(width * height);
  let fgComponents = 0;
  let holes = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const idx = y * width + x;
      if (visited[idx]) continue;
      const isFg = data[idx] !== 0;
      const neighbors = isFg
        ? [[1, 0], [-1, 0], [0, 1], [0, -1]]
        : [[1, 0], [-1, 0], [0, 1], [0, -1], [1, 1], [1, -1], [-1, 1], [-1, -1]];
      let touchesBorder = false;
      const queue = [[x, y]];
      visited[idx] = 1;
      while (queue.length > 0) {
        const [cx, cy] = queue.pop();
        if (cx === 0 || cy === 0 || cx === width - 1 || cy === height - 1) touchesBorder = true;
        for (const [dx, dy] of neighbors) {
          const nx = cx + dx;
          const ny = cy + dy;
          if (nx < 0 || ny < 0 || nx >= width || ny >= height) continue;
          const nIdx = ny * width + nx;
          if (visited[nIdx]) continue;
          if ((data[nIdx] !== 0) !== isFg) continue;
          visited[nIdx] = 1;
          queue.push([nx, ny]);
        }
      }
      if (isFg) fgComponents++;
      else if (!touchesBorder) holes++;
    }
  }
  return { fgComponents, holes };
}

// ---------- 基础情形 ----------

test('空掩码：无轮廓', () => {
  const mask = maskFromArt(['...', '...', '...']);
  const result = extractContours(mask);
  assert.equal(result.contours.length, 0);
  assert.equal(result.roots.length, 0);
  roundTrip(mask);
});

test('单像素：一条外边界，面积为 1', () => {
  const mask = maskFromArt(['.#.', '...']);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 1);
  const c = result.contours[0];
  assert.equal(c.isHole, false);
  assert.equal(c.vertices.length, 4);
  assert.equal(Math.abs(c.signedArea2), 2);
  const xs = c.vertices.map((p) => p.x).sort((a, b) => a - b);
  const ys = c.vertices.map((p) => p.y).sort((a, b) => a - b);
  assert.deepEqual([xs[0], xs[3]], [1, 2]);
  assert.deepEqual([ys[0], ys[3]], [0, 1]);
  assert.equal(result.roots.length, 1);
  assert.equal(result.nodes[0].depth, 0);
});

test('全前景：外边界即图像边框', () => {
  const mask = maskFromArt(['###', '###', '###']);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 1);
  assert.equal(Math.abs(result.contours[0].signedArea2), 18);
});

test('角上的单像素', () => {
  roundTrip(maskFromArt(['#..', '...', '...']));
  roundTrip(maskFromArt(['...', '...', '..#']));
});

// ---------- 对角接触 ----------

test('对角接触：两个独立分量、两条外边界，共享一个顶点', () => {
  const mask = maskFromArt(['#.', '.#']);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 2);
  assert.ok(result.contours.every((c) => !c.isHole));
  assert.ok(result.contours.every((c) => Math.abs(c.signedArea2) === 2));
  const keys = result.contours.map((c) => new Set(c.vertices.map((p) => `${p.x},${p.y}`)));
  assert.ok(keys[0].has('1,1') && keys[1].has('1,1'), '两条轮廓在格点 (1,1) 相接');
});

test('反对角接触', () => {
  const mask = maskFromArt(['.#', '#.']);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 2);
  assert.ok(result.contours.every((c) => !c.isHole));
});

test('对角接触不合并分量（2x3 交错）', () => {
  const mask = maskFromArt(['#.#', '.#.']);
  const result = roundTrip(mask);
  assert.equal(result.roots.length, 3);
  assert.equal(result.contours.filter((c) => c.isHole).length, 0);
});

test('背景对角穿透：缺口环不产生孔洞', () => {
  // 左下与右上的背景通过对角缝隙八连通，中心不是孔洞
  const mask = maskFromArt([
    '###',
    '#.#',
    '##.',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.filter((c) => c.isHole).length, 0);
});

// ---------- 孔洞与层级 ----------

test('3x3 环：一个孔洞，层级正确', () => {
  const mask = maskFromArt(['###', '#.#', '###']);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 2);
  const outer = result.contours.find((c) => !c.isHole);
  const hole = result.contours.find((c) => c.isHole);
  assert.equal(Math.abs(outer.signedArea2), 18);
  assert.equal(Math.abs(hole.signedArea2), 2);
  const holeIdx = result.contours.indexOf(hole);
  const outerIdx = result.contours.indexOf(outer);
  assert.equal(result.nodes[holeIdx].parent, outerIdx);
  assert.equal(result.nodes[holeIdx].depth, 1);
  assert.deepEqual(result.nodes[outerIdx].children, [holeIdx]);
});

test('孔中岛：三层嵌套深度 0/1/2', () => {
  const mask = maskFromArt([
    '#####',
    '#...#',
    '#.#.#',
    '#...#',
    '#####',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 3);
  const depths = result.nodes.map((nd) => nd.depth).sort();
  assert.deepEqual(depths, [0, 1, 2]);
  const island = result.nodes.find((nd) => nd.depth === 2);
  assert.equal(result.contours[island.index].isHole, false);
  const holeNode = result.nodes[island.parent];
  assert.equal(result.contours[holeNode.index].isHole, true);
});

test('一像素宽壁的孔洞', () => {
  const mask = maskFromArt([
    '#####',
    '#...#',
    '#.#.#',
    '#...#',
    '#####',
  ]);
  roundTrip(mask);
  const bigger = maskFromArt([
    '#######',
    '#.....#',
    '#.###.#',
    '#.#.#.#',
    '#.###.#',
    '#.....#',
    '#######',
  ]);
  const result = roundTrip(bigger);
  assert.equal(result.nodes.filter((nd) => nd.depth === 0).length, 1);
  assert.equal(result.nodes.filter((nd) => nd.depth === 1).length, 1);
  assert.equal(result.nodes.filter((nd) => nd.depth === 2).length, 1);
});

test('多个独立孔洞', () => {
  const mask = maskFromArt([
    '#########',
    '#.#...#.#',
    '#########',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.filter((c) => c.isHole).length, 3);
  assert.equal(result.roots.length, 1);
});

// ---------- 一像素宽通道 ----------

test('一像素宽前景通道（折线）', () => {
  const mask = maskFromArt([
    '####.',
    '...#.',
    '.###.',
    '.#...',
    '.####',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.length, 1, '细通道是单条外边界');
  assert.equal(result.contours[0].isHole, false);
});

test('一像素宽前景螺旋', () => {
  const mask = maskFromArt([
    '#######',
    '......#',
    '#####.#',
    '#...#.#',
    '#.###.#',
    '#.....#',
    '#######',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.roots.length, 1);
  assert.equal(result.contours.filter((c) => c.isHole).length, 0);
});

test('一像素宽背景通道贯穿厚环：无孔洞', () => {
  const mask = maskFromArt([
    '#######',
    '#.....#',
    '#.###.#',
    '#.#.#.#',
    '#.#.#.#',
    '#.#...#',
    '#.#####',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.filter((c) => c.isHole).length, 0, '通道使内部背景与外部连通');
});

test('实心环内八连通的背景区域计为一个孔洞', () => {
  const mask = maskFromArt([
    '#####',
    '#.#.#',
    '##.##',
    '#.#.#',
    '#####',
  ]);
  const result = roundTrip(mask);
  assert.equal(result.contours.filter((c) => c.isHole).length, 1);
});

// ---------- 分量计数一致性 ----------

test('轮廓数量 == 前景四连通分量 + 背景八连通孔洞', () => {
  const cases = [
    ['#.#', '.#.', '#.#'],
    ['###', '#.#', '###'],
    ['#####', '#...#', '#.#.#', '#...#', '#####'],
    ['#..#', '.##.', '.##.', '#..#'],
    ['####.', '...#.', '.###.', '.#...', '.####'],
  ];
  for (const artRows of cases) {
    const mask = maskFromArt(artRows);
    const result = roundTrip(mask);
    const { fgComponents, holes } = countComponents(mask);
    assert.equal(
      result.contours.filter((c) => !c.isHole).length,
      fgComponents,
      `外边界数应等于前景分量数\n${art(mask)}`,
    );
    assert.equal(result.contours.filter((c) => c.isHole).length, holes, `孔洞数应等于内部背景分量数\n${art(mask)}`);
  }
});

// ---------- 随机掩码性质测试 ----------

function mulberry32(seed) {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

test('随机掩码：重建核对 + 分量计数 + 不变量', () => {
  const rand = mulberry32(20261005);
  const densities = [0.15, 0.5, 0.85];
  for (let iter = 0; iter < 400; iter++) {
    const width = 1 + Math.floor(rand() * 20);
    const height = 1 + Math.floor(rand() * 20);
    const density = densities[iter % densities.length];
    const data = new Uint8Array(width * height);
    for (let k = 0; k < data.length; k++) data[k] = rand() < density ? 1 : 0;
    const mask = { width, height, data };
    const result = roundTrip(mask);
    const { fgComponents, holes } = countComponents(mask);
    assert.equal(
      result.contours.filter((c) => !c.isHole).length,
      fgComponents,
      `外边界数不匹配 iter=${iter} ${width}x${height}\n${art(mask)}`,
    );
    assert.equal(
      result.contours.filter((c) => c.isHole).length,
      holes,
      `孔洞数不匹配 iter=${iter} ${width}x${height}\n${art(mask)}`,
    );
  }
});

test('随机掩码：面积守恒（外边界面积和 - 孔洞面积和 == 前景像素数）', () => {
  const rand = mulberry32(12345);
  for (let iter = 0; iter < 200; iter++) {
    const width = 1 + Math.floor(rand() * 16);
    const height = 1 + Math.floor(rand() * 16);
    const data = new Uint8Array(width * height);
    let fg = 0;
    for (let k = 0; k < data.length; k++) {
      data[k] = rand() < 0.5 ? 1 : 0;
      if (data[k]) fg++;
    }
    const result = extractContours({ width, height, data });
    let signed = 0;
    for (const c of result.contours) signed -= c.signedArea2;
    assert.equal(signed / 2, fg, `面积不守恒 iter=${iter}`);
  }
});

// ---------- 输入校验 ----------

test('非法掩码抛错', () => {
  assert.throws(() => traceContours({ width: 2, height: 2, data: new Uint8Array(3) }));
});

test('buildHierarchy 可独立使用', () => {
  const mask = maskFromArt(['###', '#.#', '###']);
  const contours = traceContours(mask);
  const { nodes, roots } = buildHierarchy(contours);
  assert.equal(roots.length, 1);
  assert.equal(nodes.filter((nd) => nd.depth === 1).length, 1);
});

// ---------- 运行 ----------

let passed = 0;
let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    passed++;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed++;
    console.error(`FAIL ${name}`);
    console.error(err.message);
  }
}
console.log(`\n${passed} passed, ${failed} failed, ${tests.length} total`);
process.exit(failed === 0 ? 0 : 1);
