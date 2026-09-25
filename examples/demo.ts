import {
  at,
  atReverse,
  count,
  estimate,
  expand,
  stream,
  ExpandError,
  type ExpandInput,
} from "../src/index.ts";
import { verifyCompleteness } from "../src/verify.ts";

function show(title: string, value: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(
    typeof value === "string" ? value : JSON.stringify(value, null, 2),
  );
}

// 1. Two axes 2x3 -> 6 records, deterministic order (last axis fastest).
const orders: ExpandInput = {
  template: {
    sku: { $param: "size" },
    color: { $param: "color" },
    label: "tee",
  },
  templateVersion: "tpl-1",
  axes: [
    { name: "size", domain: ["S", "M"], version: "v1" },
    { name: "color", domain: ["red", "green", "blue"], version: "v1" },
  ],
};
show("2x3 expansion (6 records, fixed order)", expand(orders).records);
show("estimate (cheap upper bound)", estimate(orders));
show("count (exact)", count(orders));

// 2. Empty axis -> 0 records.
const empty: ExpandInput = {
  template: { v: { $param: "a" } },
  axes: [
    { name: "a", domain: [] },
    { name: "b", domain: [1, 2, 3] },
  ],
};
show("empty axis", expand(empty));

// 3. Finite-domain dependency + mutual exclusion with propagation.
const constrained: ExpandInput = {
  template: { cpu: { $param: "cpu" }, gpu: { $param: "gpu" } },
  axes: [
    { name: "cpu", domain: ["low", "high"] },
    { name: "gpu", domain: ["none", "small", "large"] },
  ],
  constraints: [
    {
      // dependency: a large GPU needs a high-end CPU
      kind: "implies",
      if: { param: "gpu", equals: "large" },
      then: { param: "cpu", equals: "high" },
    },
    {
      // mutual exclusion: low CPU never ships with a small GPU
      kind: "mutex",
      conditions: [
        { param: "cpu", equals: "low" },
        { param: "gpu", equals: "small" },
      ],
    },
  ],
};
show("constrained expansion (implies + mutex)", expand(constrained).records);
show("estimate vs count under constraints", {
  estimate: estimate(constrained),
  count: count(constrained),
});

// 4. Dedup by declared identity.
const dedup: ExpandInput = {
  template: { id: { $param: "k" }, tag: { $param: "t" } },
  axes: [
    { name: "k", domain: [1, 1, 2] },
    { name: "t", domain: ["a", "b"] },
  ],
  identity: ["$.id"],
};
show("dedup by declared identity $.id", expand(dedup).records);

// 5. Budget interruption -> resumable cursor; total stays null while truncated.
const page1 = expand(orders, { budget: 4 });
show("page 1 (budget=4)", {
  records: page1.records,
  complete: page1.complete,
  total: page1.total,
  cursor: page1.cursor,
});
const page2 = expand(orders, { cursor: page1.cursor! });
show("page 2 (resumed from cursor)", {
  records: page2.records,
  complete: page2.complete,
  total: page2.total,
});

// 6. Cursor is bound to the input digest: changed parameters cannot resume.
const changed: ExpandInput = {
  ...orders,
  axes: [
    { name: "size", domain: ["S", "M", "L"], version: "v1" },
    { name: "color", domain: ["red", "green", "blue"], version: "v1" },
  ],
};
try {
  expand(changed, { cursor: page1.cursor! });
} catch (err) {
  show("resume with changed parameters", (err as ExpandError).message);
}

// 7. Rank positioning and reverse ranking.
show("at(rank=4)", at(orders, 4));
show("at(rank=-1) (last)", at(orders, -1));
show("atReverse(k=0) (last)", atReverse(orders, 0));
show("atReverse(k=5) (first)", atReverse(orders, 5));

// 8. Lazy streaming: sample a huge product without expanding it.
const huge: ExpandInput = {
  template: { x: { $param: "x" }, y: { $param: "y" } },
  axes: [
    { name: "x", domain: Array.from({ length: 100000 }, (_, i) => i) },
    { name: "y", domain: Array.from({ length: 100000 }, (_, i) => i) },
  ],
};
const it = stream(huge);
const sample = [it.next().value, it.next().value, it.next().value];
show("stream: first 3 of 10^10 rows (lazy)", sample);

// 9. Small-domain exhaustive completeness check vs the naive full product.
show("verifyCompleteness(constrained)", verifyCompleteness(constrained));

// 10. Errors locate to a concrete path and source section.
try {
  expand({
    template: { items: [{ bad: { $param: "ghost" } }] },
    axes: [{ name: "a", domain: [1] }],
  });
} catch (err) {
  const e = err as ExpandError;
  show("template error", { source: e.source, path: e.path, message: e.message });
}
