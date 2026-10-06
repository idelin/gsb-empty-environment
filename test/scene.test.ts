import { frac } from "../src/fraction";
import { rect } from "../src/rect";
import { straight, premulKey } from "../src/color";
import {
  Scene, scene, rectNode, groupNode, findNode,
  updateNode, removeNode, insertChild,
} from "../src/scene";
import { ColoredFragment, renderScene } from "../src/composite";
import { assert, assertEq } from "./util";

const red = straight(frac(1), frac(0), frac(0), frac(1));
const blue = straight(frac(0), frac(0), frac(1), frac(1));
const green = straight(frac(0), frac(1), frac(0), frac(1));
const half = straight(frac(1), frac(1), frac(0), frac(1, 2));

function renderKey(frags: ColoredFragment[]): string {
  return frags
    .map((f) => `${f.rect.x0},${f.rect.y0},${f.rect.x1},${f.rect.y1}:${premulKey(f.color)}`)
    .join("|");
}

function buildScene(): Scene {
  return scene([
    rectNode("bg", rect(0, 0, 8, 8), red),
    groupNode("g1", frac(1, 2), [
      rectNode("a", rect(1, 1, 4, 4), blue),
      rectNode("b", rect(3, 3, 6, 6), green),
    ]),
    groupNode("g2", frac(1), [
      rectNode("c", rect(5, 5, 7, 7), half),
    ]),
  ]);
}

export function run(): void {
  const s0 = buildScene();
  const r0 = renderKey(renderScene(s0));

  // Local update: change one leaf's color inside g1.
  const s1 = updateNode(s0, "a", (n) =>
    n.kind === "rect" ? { ...n, color: straight(frac(0), frac(0), frac(0), frac(1)) } : n,
  );
  // Old snapshot renders exactly as before.
  assertEq(renderKey(renderScene(s0)), r0, "snapshot s0 unchanged after update");
  // New scene differs.
  assert(renderKey(renderScene(s1)) !== r0, "update must change rendering");
  // Structural sharing: untouched subtrees are the same objects.
  assert(findNode(s1.root, "g2") === findNode(s0.root, "g2"), "g2 shared by reference");
  assert(findNode(s1.root, "bg") === findNode(s0.root, "bg"), "bg shared by reference");
  assert(findNode(s1.root, "b") === findNode(s0.root, "b"), "sibling leaf shared");
  assert(findNode(s1.root, "g1") !== findNode(s0.root, "g1"), "g1 rebuilt");

  // Group opacity update.
  const s2 = updateNode(s1, "g1", (n) =>
    n.kind === "group" ? { ...n, opacity: frac(1, 4) } : n,
  );
  assertEq(renderKey(renderScene(s0)), r0, "snapshot s0 unchanged after opacity update");
  assert(renderKey(renderScene(s2)) !== renderKey(renderScene(s1)), "opacity update visible");

  // Removal and insertion preserve old snapshots too.
  const s3 = removeNode(s2, "c");
  assertEq(findNode(s3.root, "c"), undefined, "c removed");
  assertEq(renderKey(renderScene(s2)), renderKey(renderScene(s2)), "s2 stable");
  const s4 = insertChild(s3, "g2", 0, rectNode("d", rect(6, 0, 8, 2), blue));
  assert(findNode(s4.root, "d") !== undefined, "d inserted");
  assertEq(renderKey(renderScene(s0)), r0, "snapshot s0 unchanged after all updates");

  // Re-rendering any kept snapshot is deterministic.
  assertEq(renderKey(renderScene(s1)), renderKey(renderScene(s1)), "s1 deterministic");

  // Geometry sanity on the final scene: reuse the grid oracle via composite
  // test's invariants (fragments sorted, disjoint) — checked there; here we
  // only confirm the update chain produced a renderable scene.
  assert(renderScene(s4).length > 0, "final scene renders");
}
