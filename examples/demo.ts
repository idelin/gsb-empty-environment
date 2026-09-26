import { RasterImage } from "../src/raster.ts";
import { makeTestImage, rgbaToLabels, labelsToRgba } from "../src/rgba.ts";
import { RefGrid } from "../src/refgrid.ts";

const { img, palette } = makeTestImage();
const labels = rgbaToLabels(img, palette);
console.log(`built RGBA test image ${img.width}x${img.height}, palette=${palette.length} colors`);

const raster = new RasterImage(img.width, img.height, {
  labels: palette.map((_, i) => i),
  blockRows: 3,
});
const ref = new RefGrid(img.width, img.height, 0);

raster.writeRect(0, 0, img.width, img.height, labels);
ref.writeRect(0, 0, img.width, img.height, labels);

const snap0 = raster.snapshot();

raster.resetStats();
const p1 = raster.writeRect(4, 2, 3, 5, 4);
ref.writeRect(4, 2, 3, 5, 4);
console.log(`writeRect 3x5 -> decoded rows: ${raster.stats.rowsDecoded} (image has ${img.height})`);
console.log(`patch baseline: seq ${p1.baseline.baseSeq}->${p1.baseline.resultSeq}, affectedRows=${p1.baseline.affectedRows}`);

const p2 = raster.translateRegion(2, 1, 5, 4, 3, 3);
ref.translateRegion(2, 1, 5, 4, 3, 3);
console.log(`overlapping translate (+3,+3) -> affectedRows=${p2.baseline.affectedRows}`);

const mask = [1, 0, 1, 0, 1, 0, 0, 1, 1, 0, 1, 0];
const p3 = raster.applyMask(8, 6, 4, 3, mask, 1);
ref.applyMask(8, 6, 4, 3, mask, 1);

const got = raster.readRect(0, 0, img.width, img.height);
const want = ref.all();
const mismatches = got.filter((v, i) => v !== want[i]).length;
console.log(`pixel-by-pixel vs reference: ${got.length} pixels, mismatches=${mismatches}`);

const rgba = labelsToRgba(img.width, img.height, got, palette);
console.log(`re-encoded RGBA bytes: ${rgba.data.length}`);

const sharing = raster.compareWithSnapshot(snap0);
console.log(`blocks shared with snapshot #${snap0}: ${sharing.shared}/${sharing.total}`);

raster.applyPatch(p3, true);
raster.applyPatch(p2, true);
console.log(`inverse patches of mask+translate applied (LIFO), validate() ok`);
raster.validate();

try {
  raster.writeRect(0, 0, 2, 2, [0, 1, 99, 1]);
} catch (e) {
  console.log(`invalid label mid-write rolled back: ${(e as Error).message}`);
}
raster.validate();
console.log("demo finished OK");
