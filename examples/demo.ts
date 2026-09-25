import { Raster } from "../src/raster.ts";
import {
  buildPalette,
  compareImages,
  imageToLabels,
  labelsToImage,
  makeTestImage,
} from "../src/rgba.ts";

const W = 16;
const H = 12;

function printGrid(title: string, labels: number[]): void {
  console.log(title);
  for (let y = 0; y < H; y++) {
    console.log("  " + labels.slice(y * W, (y + 1) * W).join(""));
  }
}

// 1. 自建 RGBA 小图（不依赖 Canvas / 图像库），调色板映射为标签
const img = makeTestImage(W, H);
const palette = buildPalette(img);
const labels = imageToLabels(img, palette);
console.log(`palette: ${palette.length} colors -> labels 0..${palette.length - 1}`);

// 2. 载入栅格（逐行游程存储）
const raster = new Raster(W, H, { labelCount: palette.length, blockSize: 4 });
raster.writeRect(0, 0, W, H, labels);
raster.resetStats();
printGrid("initial:", raster.readRect(0, 0, W, H));

// 3. 历史快照 + 一系列编辑，各自产生可逆补丁
const snap0 = raster.snapshot();
const p1 = raster.fillRect(2, 2, 6, 4, 1);
const p2 = raster.translate(2, 2, 6, 4, 4, 3);
const mask: boolean[] = [];
for (let i = 0; i < 5 * 5; i++) mask.push(i % 2 === 0);
const p3 = raster.applyMask(8, 1, 5, 5, mask, 2);
const snap1 = raster.snapshot();
printGrid("after fill + overlapping translate + mask:", raster.readRect(0, 0, W, H));

for (const p of [p1, p2, p3]) {
  console.log(
    `patch #${p.seq} ${p.kind}: rows=[${p.changes.map((c) => c.row).join(",")}] ` +
      `baseline#${p.baseline.checksum.toString(16)} -> result#${p.result.checksum.toString(16)}`,
  );
}

// 4. 快照间共享块统计（写时复制，未改动的行数组直接共享）
const share = raster.sharedBlocks(snap0, snap1);
console.log(
  `snapshots share ${share.sharedBlocks}/${share.totalBlocks} blocks, ` +
    `${share.sharedRows}/${share.totalRows} rows`,
);
console.log(`rows decoded so far: ${raster.stats.rowsDecoded} (of ${H})`);

// 5. 逆补丁逐步撤销，回到初始状态并逐像素比对
raster.applyPatch(p3, { reverse: true });
raster.applyPatch(p2, { reverse: true });
raster.applyPatch(p1, { reverse: true });
const restored = labelsToImage(raster.readRect(0, 0, W, H), W, H, palette);
const diff = compareImages(img, restored);
if (!diff.equal) {
  console.error("MISMATCH after inverse patches:", diff.firstDiffs);
  process.exit(1);
}
printGrid("after applying inverse patches:", raster.readRect(0, 0, W, H));

// 6. 快照恢复 + 完整性校验
raster.restore(snap0);
const report = raster.validate();
console.log(`validate: ok=${report.ok}, block totals=[${raster.blockTotals().join(",")}]`);
console.log("DEMO OK");
