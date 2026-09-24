import {
  ConflictError,
  PatchApplyError,
  TextDocument,
  applyPatch,
  composePatches,
  createIdGen,
  diffPatch,
  digestText,
  invertPatch,
  makePatch,
  rebasePatch,
  type Op,
} from "../src/index.ts";

const ids = createIdGen("demo");
const show = (label: string, value: unknown): void =>
  console.log(`${label.padEnd(28)}`, value);

console.log("=== 1. Generate a context-validated patch (UTF-16 offsets) ===");
const base = "hello 😀 world\r\nfoo foo foo";
const edited = "hello 😀 there\r\nfoo foo";
const p = diffPatch(base, edited, ids);
show("base:", JSON.stringify(base));
show("edited:", JSON.stringify(edited));
show("patch ops:", p.ops);
show("baseDigest:", p.baseDigest.slice(0, 16) + "…");
show("apply(P):", JSON.stringify(applyPatch(base, p)));

console.log("\n=== 2. Invert: P then inverse(P) restores byte-exact original ===");
const inv = invertPatch(base, p, ids);
const restored = applyPatch(edited, inv);
show("apply(inverse(P)):", JSON.stringify(restored));
show("byte-exact:", Buffer.from(restored, "utf8").equals(Buffer.from(base, "utf8")));

console.log("\n=== 3. Compose: P∘Q equals sequential application ===");
const q = diffPatch(edited, "hello 😀 there\nfoo", ids);
const composed = composePatches(p, q);
show("sequential:", JSON.stringify(applyPatch(applyPatch(base, p), q)));
show("composed:", JSON.stringify(applyPatch(base, composed)));
const roundTrip = composePatches(p, invertPatch(base, p, ids));
show("P∘inverse(P) ops:", roundTrip.ops);

console.log("\n=== 4. Atomic failure: same-length impostor base is rejected ===");
const doc = new TextDocument(base, ids);
const impostor = base.replace("foo", "bar"); // same length, different content
show("impostor same length:", impostor.length === base.length);
try {
  applyPatch(impostor, p);
} catch (err) {
  show("rejected with:", (err as Error).name);
}
try {
  doc.apply({ ...p, baseDigest: "0".repeat(64) }); // forged digest
} catch (err) {
  show("forged digest rejected:", err instanceof PatchApplyError);
}
show("document untouched:", doc.getText() === base);

console.log("\n=== 5. Three-way relocation of concurrent edits ===");
const shared = "alpha\nbeta\ngamma\n";
const editA = diffPatch(shared, "alpha\nBETA\ngamma\n", createIdGen("A"));
const editB = diffPatch(shared, "alpha\nbeta\ngamma\ndelta\n", createIdGen("B"));
const aPrime = rebasePatch(shared, editA, editB);
const bPrime = rebasePatch(shared, editB, editA);
const viaA = applyPatch(applyPatch(shared, editA), bPrime);
const viaB = applyPatch(applyPatch(shared, editB), aPrime);
show("A then B':", JSON.stringify(viaA));
show("B then A':", JSON.stringify(viaB));
show("swap diagram closes:", viaA === viaB);

console.log("\n=== 6. Crossing deletions conflict ===");
// shared = "alpha\nbeta\ngamma\n" (17 UTF-16 units)
const mk = (ops: Op[]) => makePatch(shared.length, digestText(shared), ops);
const delA = mk([
  { kind: "retain", count: 2 },
  { kind: "delete", count: 6 }, // deletes offsets 2..8
  { kind: "retain", count: 9 },
]);
const delB = mk([
  { kind: "retain", count: 4 },
  { kind: "delete", count: 6 }, // deletes offsets 4..10 (overlaps delA)
  { kind: "retain", count: 7 },
]);
try {
  rebasePatch(shared, delA, delB);
  show("conflict:", "NOT DETECTED (bug)");
} catch (err) {
  show("conflict:", err instanceof ConflictError && (err as Error).message);
}

console.log("\n=== 7. Insert identity: same text, distinct edits ===");
const insA = diffPatch("ab", "aXXb", createIdGen("userA"));
const insB = diffPatch("ab", "aXXb", createIdGen("userB"));
const idsOf = (ops: Op[]) =>
  ops.filter((op) => op.kind === "insert").map((op) => (op.kind === "insert" ? op.id : ""));
show("insert ids:", [...idsOf(insA.ops), ...idsOf(insB.ops)]);
const merged = applyPatch(applyPatch("ab", insA), rebasePatch("ab", insB, insA));
show("both inserts survive:", JSON.stringify(merged));

console.log("\n=== 8. Surrogate-pair protection ===");
try {
  applyPatch("a😀b", {
    baseLength: 4,
    baseDigest: digestText("a😀b"),
    ops: [
      { kind: "retain", count: 2 }, // cuts the 😀 surrogate pair in half
      { kind: "insert", id: "x", text: "!" },
      { kind: "retain", count: 2 },
    ],
  });
} catch (err) {
  show("split pair rejected:", err instanceof PatchApplyError && (err as Error).message);
}
