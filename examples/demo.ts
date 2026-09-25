import { extractContours } from "../src/contours.ts";
import { fillRings } from "../src/fill.ts";
import { masksEqual, maskToAscii } from "../src/mask.ts";
import { RgbaImage } from "../src/rgba-image.ts";

// Build a small RGBA image by hand: a rectangle with a hole, a bar touching
// the image edge, and two pixels in diagonal contact.
const img = new RgbaImage(16, 12);
img.fillRect(1, 1, 6, 5); // rectangle
img.clear(3, 2); // 1x1 hole
img.clear(4, 2);
img.fillRect(9, 0, 7, 2); // bar touching the top/right edge
img.fg(10, 5); // diagonal pair
img.fg(11, 6);

const mask = img.toMask();
console.log("input mask (# = foreground):");
console.log(maskToAscii(mask));
console.log();

const rings = extractContours(mask);
console.log(`extracted ${rings.length} closed ring(s):`);
for (const [i, ring] of rings.entries()) {
  const vs = ring.vertices.map((p) => `(${p.x},${p.y})`).join(" ");
  const ps = ring.pixels.map((p) => `(${p.x},${p.y})`).join(" ");
  console.log(
    `#${i} ${ring.kind}  area=${ring.signedArea}  perimeter=${ring.perimeter}`,
  );
  console.log(`   vertices: ${vs}`);
  console.log(`   boundary pixels: ${ps}`);
}
console.log();

const refilled = fillRings(rings, mask.width, mask.height);
console.log("refilled from rings:");
console.log(maskToAscii(refilled));
console.log();
console.log(
  masksEqual(refilled, mask)
    ? "OK: filling the rings reproduces the original mask"
    : "MISMATCH: refill differs from the original mask",
);
