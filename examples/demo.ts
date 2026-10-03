/**
 * Runnable demo: build an exact Euclidean distance field from a hand-made
 * RGBA image, edit it incrementally, and query path clearance.
 *
 * Run with:  npm run example
 */

import { DistanceField } from "../src/distanceField.ts";
import { createImage, setPixel, fillRect, obstaclesFromImage } from "../src/rgba.ts";

const W = 48, H = 20;

// 1. Hand-build a small RGBA image (no canvas, no image library): opaque
//    pixels are obstacles.
const img = createImage(W, H);
fillRect(img, 8, 6, 3, 8, 255, 60, 60, 255);    // vertical wall
fillRect(img, 30, 4, 8, 3, 60, 60, 255, 255);   // horizontal block
setPixel(img, 20, 15, 255, 255, 0, 255);        // single pixel
const obstacles = obstaclesFromImage(img);

// 2. Build the exact squared-distance field.
const field = DistanceField.fromObstacles(W, H, obstacles);
const v1 = field.retain(field.version);

function render(v: typeof v1): string {
  const shades = " .:-=+*#%@";
  let out = "";
  for (let y = 0; y < H; y++) {
    let row = "";
    for (let x = 0; x < W; x++) {
      const c = field.cellAt(v, x, y);
      if (c.obstacle) { row += "O"; continue; }
      const d = Math.sqrt(c.dist);
      row += shades[Math.min(shades.length - 1, Math.floor(d))];
    }
    out += row + "\n";
  }
  return out;
}

console.log("=== distance field from RGBA image (version " + v1.id + ") ===");
console.log(render(v1));
console.log("clearance at (4,10):", field.clearanceAt(v1, 4.5, 10.5).toFixed(3));

// 3. Incremental edit: remove the wall, move the block. Only affected
//    regions are repaired; nothing is visible until commit.
const session = field.beginEdit();
for (let y = 6; y < 14; y++) for (let x = 8; x < 11; x++) session.setObstacle(x, y, false);
session.moveRegion(30, 4, 8, 3, -14, 8); // move the block by (-14,+8)
console.log("mid-edit, published version is still", field.version.id,
  "-> cell (9,10) dist:", field.cellAt(field.version, 9, 10).dist);
const v2 = session.commit();

console.log("=== after incremental edit (version " + v2.id + ") ===");
console.log(render(v2));

// 4. Old version is still queryable.
console.log("old version cell (4,10): dist^2 =", field.cellAt(v1, 4, 10).dist,
  "src =", field.cellAt(v1, 4, 10).srcX + "," + field.cellAt(v1, 4, 10).srcY);
console.log("new version cell (4,10): dist^2 =", field.cellAt(v2, 4, 10).dist,
  "src =", field.cellAt(v2, 4, 10).srcX + "," + field.cellAt(v2, 4, 10).srcY);

// 5. Path clearance query (samples the whole path, not just endpoints).
const path = [{ x: 2.5, y: 2.5 }, { x: 45.5, y: 17.5 }];
const pc = field.pathClearance(v2, path);
console.log("path clearance:", pc.clearance.toFixed(3), "at", pc.at.x.toFixed(1) + "," + pc.at.y.toFixed(1));
console.log("path clear for radius 1.5?", field.isPathClear(v2, path, 1.5));
console.log("path clear for radius 3.0?", field.isPathClear(v2, path, 3.0));

// 6. Cancel rolls back; garbage collection reclaims unreferenced pages.
const tmp = field.beginEdit();
tmp.setObstacle(40, 10, true);
tmp.cancel();
console.log("after cancel, version is still", field.version.id);

field.release(v1);
console.log("pages before GC:", field.pageCount, "-> reclaimed:", field.collectGarbage(),
  "-> pages after GC:", field.pageCount);
