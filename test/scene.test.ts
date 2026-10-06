import { rgb } from "../src/color";
import { Rational } from "../src/rational";
import { rect } from "../src/rect";
import {
  findNode,
  flattenScene,
  layerGroup,
  rectLayer,
  removeNode,
  setGroupOpacity,
  updateRectLayer,
} from "../src/scene";
import { eq, ok, test } from "./harness";

const red = rgb(Rational.ONE, Rational.ZERO, Rational.ZERO);
const blue = rgb(Rational.ZERO, Rational.ZERO, Rational.ONE);

function snapshotKey(root: Parameters<typeof flattenScene>[0]): string {
  return flattenScene(root)
    .map(
      (l) =>
        `${l.id}@${l.rect.x0},${l.rect.y0},${l.rect.x1},${l.rect.y1} a=${l.alpha}`,
    )
    .join(" | ");
}

function buildScene() {
  return layerGroup("root", Rational.ONE, [
    layerGroup("g1", Rational.of(1, 2), [
      rectLayer("r1", rect(0, 0, 4, 4), red, Rational.ONE),
      rectLayer("r2", rect(2, 2, 6, 6), blue, Rational.of(1, 2)),
    ]),
    rectLayer("r3", rect(4, 0, 8, 2), red, Rational.of(3, 4)),
  ]);
}

test("scene: local updates preserve old snapshots", () => {
  const root = buildScene();
  const before = snapshotKey(root);

  const updated = updateRectLayer(root, "r2", { alpha: Rational.of(1, 4) });
  ok(updated !== root, "update returns a new root");
  eq(snapshotKey(root), before, "old snapshot unchanged after updateRectLayer");

  const regrouped = setGroupOpacity(root, "g1", Rational.of(1, 3));
  eq(snapshotKey(root), before, "old snapshot unchanged after setGroupOpacity");

  const removed = removeNode(root, "r1");
  eq(snapshotKey(root), before, "old snapshot unchanged after removeNode");

  // new roots reflect the changes
  const flat = flattenScene(updated);
  const r2 = flat.find((l) => l.id === "r2")!;
  ok(r2.alpha.equals(Rational.of(1, 8)), "r2 effective alpha = 1/2 * 1/4");
  const g1 = flattenScene(regrouped).find((l) => l.id === "r1")!;
  ok(g1.alpha.equals(Rational.of(1, 3)), "r1 effective alpha follows group");
  ok(findNode(removed, "r1") === null, "r1 gone in new root");
  ok(findNode(root, "r1") !== null, "r1 still in old root");
});

test("scene: updates share untouched subtrees", () => {
  const root = buildScene();
  const updated = updateRectLayer(root, "r2", { rect: rect(1, 1, 3, 3) });
  if (root.kind !== "group" || updated.kind !== "group") throw new Error("bad");
  ok(updated.children[1] === root.children[1], "sibling branch shared by reference");
  ok(updated.children[0] !== root.children[0], "touched branch rewritten");
  const regrouped = setGroupOpacity(root, "g1", Rational.of(1, 5));
  if (regrouped.kind !== "group") throw new Error("bad");
  ok(regrouped.children[1] === root.children[1], "opacity change shares siblings");
});

test("scene: flatten applies nested group opacity multiplicatively", () => {
  const scene = layerGroup("outer", Rational.of(1, 2), [
    layerGroup("inner", Rational.of(1, 3), [
      rectLayer("x", rect(0, 0, 1, 1), red, Rational.of(1, 4)),
    ]),
  ]);
  const flat = flattenScene(scene);
  eq(flat.length, 1, "one layer");
  ok(flat[0].alpha.equals(Rational.of(1, 24)), "1/2 * 1/3 * 1/4 = 1/24");
});
