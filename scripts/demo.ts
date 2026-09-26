/**
 * End-to-end runnable sample using only hand-built RGBA images (no Canvas,
 * no image libraries). Run with:  node scripts/demo.ts
 */
import {
  buildPalette,
  COLOR_SPACE_VERSION,
  DiffusionEncoder,
  encodeIndexed,
  frontierFromJSON,
  makeImage,
  reconstruct,
  reconstructionError,
  revisePixels,
  cloneImage,
} from "../src/index.ts";

function line(title: string) {
  console.log("\n=== " + title + " ===");
}

// 6x4 image with a vertical transparent barrier column at x = 3.
const px = [];
let k = 0;
for (let y = 0; y < 4; y++) {
  for (let x = 0; x < 6; x++) {
    if (x === 3) {
      px.push([0, 0, 0, 0]);
    } else {
      px.push([(k * 47) % 256, (k * 89) % 256, (k * 31) % 256, 255]);
    }
    k++;
  }
}
const image = makeImage(6, 4, px);

line(`color-space version: ${COLOR_SPACE_VERSION}`);

line("1) fixed-size palette (K=4), transparent pixels excluded");
const pr = buildPalette(image, { colorCount: 4 });
console.log("method         :", pr.method, pr.certifiedBest ? "(certified optimal)" : "(heuristic, NOT optimal)");
console.log("palette        :", pr.palette.map((c) => `rgb(${c[0]},${c[1]},${c[2]})`).join(" "));
console.log("opaque colors  :", pr.distinctOpaqueColors, "| transparent pixels never counted");
console.log("fingerprint    :", pr.fingerprint);

line("2) indexed image without / with Floyd-Steinberg diffusion");
const none = encodeIndexed(image, pr.palette, { method: "none" });
const fs = encodeIndexed(image, pr.palette, { method: "floyd-steinberg" });
const grid = (indices: Uint16Array) => {
  for (let y = 0; y < 4; y++) {
    console.log(
      Array.from(indices.subarray(y * 6, (y + 1) * 6))
        .map((v) => (v === pr.palette.length ? "." : String(v)))
        .join(" ")
    );
  }
};
console.log("nearest-only:");
grid(none.indices);
console.log("floyd-steinberg ('.' = transparent sentinel, barrier column):");
grid(fs.indices);

line("3) reconstruction error");
const report = reconstructionError(image, fs);
console.log(report);

line("4) chunked streaming gives identical output (chunks of 1 row)");
const enc = new DiffusionEncoder(image, pr.palette);
const streamed: number[] = [];
while (!enc.done) {
  const part = enc.processRows(1);
  streamed.push(...Array.from(part.indices));
}
console.log("identical to one-shot:", JSON.stringify(streamed) === JSON.stringify(Array.from(fs.indices)));

line("5) save / restore the diffusion frontier mid-image");
const enc2 = new DiffusionEncoder(image, pr.palette);
enc2.processRows(2);
const saved = enc2.toJSON();
const restored = DiffusionEncoder.restore(image, pr.palette, frontierFromJSON(saved));
while (!restored.done) restored.processRows(2);
console.log("restored result identical:",
  JSON.stringify(Array.from(restored.result().indices)) === JSON.stringify(Array.from(fs.indices)));
console.log("saved frontier rows:", frontierFromJSON(saved).row, "version:", frontierFromJSON(saved).colorSpaceVersion);

line("6) local pixel revision recomputes the diffusion cone");
const edited = cloneImage(image);
const working = encodeIndexed(cloneImage(image), pr.palette);
const revision = revisePixels(edited, working, [{ x: 0, y: 0, rgba: [255, 0, 0, 255] }]);
const full = encodeIndexed(edited, pr.palette);
console.log("changed pixels:", revision.changedPixels, "| recomputed cone pixels:", revision.affectedPixels);
console.log("cone revision == full re-encode:",
  JSON.stringify(Array.from(revision.indices)) === JSON.stringify(Array.from(full.indices)));

line("7) heuristic gap report on a small color domain");
const small = makeImage(1, 6, [
  [0, 0, 0, 255],
  [1, 1, 1, 255],
  [100, 100, 100, 255],
  [102, 102, 102, 255],
  [250, 250, 250, 255],
  [252, 252, 252, 255],
]);
const h = buildPalette(small, { colorCount: 2, forceHeuristic: true });
console.log("heuristic SSE:", h.sse, "| exact SSE:", h.exactSse,
  "| relative gap:", h.gapVsExact === null ? "n/a" : (h.gapVsExact * 100).toFixed(2) + "%",
  "| certifiedBest:", h.certifiedBest);

line("8) K=1 defined; no opaque pixels defined");
console.log("K=1 palette:", buildPalette(makeImage(1, 2, [[5, 5, 5, 255], [7, 7, 7, 255]]), { colorCount: 1 }).palette);
console.log("all-transparent K=2 palette:", buildPalette(makeImage(1, 1, [[0, 0, 0, 0]]), { colorCount: 2 }).palette);
void reconstruct;
