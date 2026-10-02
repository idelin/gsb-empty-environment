import { DominatorTree } from "../src/dominator.ts";

function showIdoms(tree: DominatorTree, nodes: string[]): void {
  for (const n of nodes) {
    console.log(`  idom(${n}) = ${tree.idom(n) ?? "null"}`);
  }
}

// Diamond CFG: S branches to A and B, both join at T.
const tree = new DominatorTree("S", [
  ["S", "A"],
  ["S", "B"],
  ["A", "T"],
  ["B", "T"],
]);

console.log("1) Diamond S -> {A, B} -> T");
showIdoms(tree, ["S", "A", "B", "T"]);

console.log("\n2) Insert bypass edge S -> T");
tree.addEdge("S", "T");
showIdoms(tree, ["T"]);

console.log("\n3) Remove bypass edge S -> T, then remove edge B -> T");
tree.removeEdge("S", "T");
tree.removeEdge("B", "T");
showIdoms(tree, ["T"]);

console.log("\n4) Counterexample: does A dominate T in the diamond?");
const diamond = new DominatorTree("S", [
  ["S", "A"],
  ["S", "B"],
  ["A", "T"],
  ["B", "T"],
]);
const witness = diamond.proveNotDominates("A", "T");
console.log(
  witness
    ? `  No. Witness path avoiding A: ${witness.join(" -> ")}`
    : "  Yes, A dominates T.",
);
console.log(
  `  Does S dominate T? ${diamond.proveNotDominates("S", "T") === null ? "Yes (no counterexample)" : "No"}`,
);
