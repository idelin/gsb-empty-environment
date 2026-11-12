/**
 * End-to-end usage: declare a sanitization contract, model the implementation
 * control flow, verify it path by path, and consume only the sealed summary.
 */
import {
  annotate,
  compare,
  constOperand,
  field,
  implement,
  labelRef,
  ref,
  requireVerified,
  resetRegistry,
  useSummary,
  verify,
} from "../src/index.ts";

resetRegistry();

const contract = annotate("escapeHtml", {
  label: { kind: "label", labels: ["raw", "escaped"] },
  external: { kind: "bool" },
})
  .returns(
    ["escaped"],
    [compare("eq", field("label"), constOperand("escaped"))],
  )
  .throws("EscapeError", [
    compare("eq", field("label"), constOperand("escaped")),
  ])
  .build();

const implementation = implement("escapeHtml", "entry")
  .node({ kind: "entry", id: "entry", next: "gate" })
  .node({
    kind: "branch",
    id: "gate",
    guard: compare("eq", field("external"), constOperand(true)),
    trueNext: "clean",
    falseNext: "fail",
  })
  .node({
    kind: "effects",
    id: "clean",
    effects: [labelRef(ref("label"), "escaped")],
    next: "done",
  })
  .node({ kind: "return", id: "done", ref: ref("label") })
  .node({
    kind: "effects",
    id: "fail",
    effects: [labelRef(ref("label"), "escaped")],
    next: "error",
  })
  .node({ kind: "throw", id: "error", tag: "EscapeError", handler: null })
  .build();

const result = verify(contract, implementation);
if (!result.ok) {
  for (const counterexample of result.counterexamples) {
    console.error(`${counterexample.reason}: ${counterexample.detail}`);
  }
  process.exitCode = 1;
} else {
  // Callers can only get here through the sealed, verified summary.
  const summary = useSummary(requireVerified(result));
  console.log(`verified contract '${summary.id}' with ${summary.exits.length} exits`);
}
