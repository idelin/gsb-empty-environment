import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { extractContours, fillContours, masksEqual, type Contour } from "../src/contours.ts";
import { maskToRgba, rasterizeRgba, rgbaToMask } from "../src/image.ts";

const projectRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const sampleDirectory = join(projectRoot, "samples");

const art = [
  "#...........",
  "............",
  "......#####.",
  "......#...#.",
  "......#.#.#.",
  "......#...#.",
  "......#####.",
  ".#..........",
  "..#.........",
];

const palette = {
  ".": [18, 18, 24, 0] as const,
  "#": [68, 210, 155, 255] as const,
};

const image = rasterizeRgba(art, palette);
const originalMask = rgbaToMask(image);
const contours = extractContours(image);
const filledMask = fillContours(contours, image.width, image.height);
const roundTripImage = maskToRgba(filledMask, image.width, image.height, [68, 210, 155, 255], [18, 18, 24, 0]);

if (!masksEqual(originalMask, filledMask)) {
  throw new Error("sample fill round trip failed");
}

const report = {
  image: {
    width: image.width,
    height: image.height,
    channels: 4,
    format: "raw RGBA8, top-to-bottom rows",
    foreground: "alpha >= 128",
  },
  inputAscii: art,
  contourCount: contours.length,
  contours: contours.map((contour: Contour) => ({
    id: contour.id,
    kind: contour.kind,
    signedArea: contour.signedArea,
    vertices: contour.vertices,
    edges: contour.edges,
  })),
  roundTripMatchesInput: true,
};

await mkdir(sampleDirectory, { recursive: true });
await writeFile(join(sampleDirectory, "sample-12x9.rgba"), image.data);
await writeFile(join(sampleDirectory, "sample-roundtrip-12x9.rgba"), roundTripImage.data);
await writeFile(join(sampleDirectory, "contours.json"), `${JSON.stringify(report, null, 2)}\n`);

console.log(`RGBA image: ${image.width}x${image.height}, raw RGBA8`);
console.log("Input:");
for (const line of art) {
  console.log(`  ${line}`);
}
console.log(`\nclosed walks: ${contours.length}`);
for (const contour of contours) {
  const walk = contour.vertices.map((point) => `(${point.x},${point.y})`).join(" -> ");
  console.log(`#${contour.id} ${contour.kind} area=${contour.signedArea}`);
  console.log(`  ${walk} -> (${contour.vertices[0].x},${contour.vertices[0].y})`);
}
console.log("\nfill contours with nonzero winding: matches original RGBA mask");
console.log("wrote samples/sample-12x9.rgba, samples/sample-roundtrip-12x9.rgba, samples/contours.json");
