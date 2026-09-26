/**
 * 运行样例：9x7 渐变 RGBA 小图（自建，无图像库/Canvas）。
 * 演示：层尺寸、逐层降采样、局部编辑的祖先失效、跨层区域读、缓存统计。
 */
import { createImage, referencePyramid } from '../src/image.ts';
import { TilePyramid } from '../src/pyramid.ts';

function printChannel(label: string, data: Uint8Array, w: number, h: number, c: number): void {
  console.log(label);
  for (let y = 0; y < h; y++) {
    const row: string[] = [];
    for (let x = 0; x < w; x++) row.push(String(data[(y * w + x) * 4 + c]).padStart(3));
    console.log('  ' + row.join(' '));
  }
}

// 9x7 渐变图：R 随 x 变化，G 随 y 变化，B 为棋盘
const img = createImage(9, 7, (x, y) => [x * 28, y * 36, (x + y) % 2 === 0 ? 200 : 40, 255]);
const pyr = new TilePyramid(img, { tileSize: 4, maxCacheBytes: 1 << 16 });

console.log('== 层尺寸（ceil(n / 2^L)）==');
console.log(pyr.levels.map((l) => `${l.width}x${l.height}`).join(' -> '));

console.log('\n== L0 红色通道 ==');
const l0 = await pyr.readRegion(0, 0, 0, 9, 7);
printChannel('', l0.data, 9, 7, 0);

console.log('\n== L1 红色通道（5x4，右/下边缘按实际覆盖加权）==');
const l1 = await pyr.readRegion(1, 0, 0, 5, 4);
printChannel('', l1.data, 5, 4, 0);

console.log('\n== 与独立参考实现逐字节比对 ==');
const ref = referencePyramid(img);
let allMatch = true;
for (let l = 0; l < ref.length; l++) {
  const r = await pyr.readRegion(l, 0, 0, 99, 99);
  const match = r.data.every((v, i) => v === ref[l].data[i]);
  allMatch &&= match;
  console.log(`L${l}: ${match ? 'MATCH' : 'MISMATCH'}`);
}
console.log(allMatch ? '全部层一致' : '存在不一致!');

console.log('\n== 局部编辑 (8,6) -> 红色，仅失效祖先链 ==');
const invalidated = pyr.setPixel(8, 6, 255, 0, 0, 255);
console.log('失效瓦片:', invalidated.join(' '));
const before = pyr.statsSnapshot().computes;
const l1b = await pyr.readRegion(1, 0, 0, 5, 4);
printChannel('L1 红色通道（右下角已更新）', l1b.data, 5, 4, 0);
console.log(`本次读取重算瓦片数: ${pyr.statsSnapshot().computes - before}（祖先链长度 ${invalidated.length}）`);

console.log('\n== 跨层区域读：基准层 (0,0,9,7) 输出 3x2 ==');
const scaled = await pyr.readScaled(0, 0, 9, 7, 3, 2);
console.log(`选取层: L${scaled.level}（${pyr.levelWidth(scaled.level)}x${pyr.levelHeight(scaled.level)}）`);
printChannel('红色通道', scaled.data, 3, 2, 0);

console.log('\n== 缓存/并发统计 ==');
const s = pyr.statsSnapshot();
console.log(`hits=${s.hits} misses=${s.misses} computes=${s.computes} evictions=${s.evictions}`);
console.log(`invalidations=${s.invalidations} cancels=${s.cancels} releasedBytes=${s.releasedBytes}`);
console.log(`currentBytes=${s.currentBytes} peakBytes=${s.peakBytes}`);
