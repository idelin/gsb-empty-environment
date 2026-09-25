import { RGBAImage } from "../src/rgba-image.ts";
import { ComponentLabeler, type Connectivity } from "../src/labeler.ts";

const LETTERS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";

function render(labeler: ComponentLabeler): string {
  const names = new Map<number, string>();
  const lines: string[] = [];
  for (let y = 0; y < labeler.height; y++) {
    let line = "";
    for (let x = 0; x < labeler.width; x++) {
      const id = labeler.labelAt(x, y);
      if (id === 0) {
        line += ".";
        continue;
      }
      if (!names.has(id)) names.set(id, LETTERS[names.size % LETTERS.length]);
      line += names.get(id);
    }
    lines.push(line);
  }
  const legend = [...names.entries()]
    .map(([id, ch]) => `${ch}=id${id}(size=${labeler.componentSize(id)})`)
    .join("  ");
  return lines.join("\n") + "\n" + legend;
}

function show(title: string, labeler: ComponentLabeler): void {
  console.log(`\n== ${title} ==`);
  console.log(render(labeler));
}

// --- Part 1: diagonal pixels, 4- vs 8-connectivity -----------------------
const diagonal = RGBAImage.fromAscii([
  "#.",
  ".#",
]);
show("diagonal pixels, 4-connectivity (separate)", new ComponentLabeler(diagonal, 4));
show("diagonal pixels, 8-connectivity (joined)", new ComponentLabeler(diagonal, 8));

// --- Part 2: merge / split / stability on one image ----------------------
const img = RGBAImage.fromAscii([
  "##.##....",
  "##.##....",
  ".........",
  ".......##",
  ".......##",
]);
const conn: Connectivity = 4;
const labeler = new ComponentLabeler(img, conn);
show(`initial image, ${conn}-connectivity`, labeler);

console.log("\n>> paint bridge pixel (2,0): blocks A and B merge, larger/equal keeps its ID");
img.setForeground(2, 0, true);
labeler.updatePixel(img, 2, 0);
show("after adding bridge (2,0)", labeler);

console.log("\n>> erase bridge pixel (2,0): component splits, anchor side keeps the ID");
img.setForeground(2, 0, false);
labeler.updatePixel(img, 2, 0);
show("after removing bridge (2,0)", labeler);

console.log("\n>> paint an isolated pixel far away: existing IDs stay stable");
img.setForeground(4, 4, true);
labeler.updatePixel(img, 4, 4);
show("after adding isolated pixel (4,4)", labeler);

console.log("\n>> extend block A downward: only A changes, B and C keep their IDs");
img.setForeground(0, 2, true);
labeler.updatePixel(img, 0, 2);
show("after extending A at (0,2)", labeler);
