/**
 * Runnable demo: `node examples/demo.ts` (or `npm run demo`).
 * Requires Node >= 23.6 (native TypeScript type stripping).
 */

import {
  CompositeUniqueChecker,
  RecordValidationError,
  fromChunks,
  type Schema,
} from "../src/index.ts";

function show(title: string, value: unknown): void {
  console.log(`\n=== ${title} ===`);
  console.log(JSON.stringify(value, null, 2));
}

// --- 1. Duplicates across chunk boundaries ---------------------------------
const chunks = [
  [{ id: 1, name: "ada" }, { id: 2, name: "grace" }],
  [{ id: 3, name: "alan" }],
  [{ id: 1, name: "ada" }, { id: 4, name: "edsger" }, { id: 3, name: "alan" }],
];
const acrossChunks = await new CompositeUniqueChecker({ fields: ["id", "name"] }).check(
  fromChunks(chunks),
);
show("1. duplicates across chunk boundaries", acrossChunks);

// --- 2. Separator-containing values cannot collide --------------------------
const tricky = [
  { a: "x|y", b: "z" },
  { a: "x", b: "y|z" }, // naive "x|y|z" string-join would collide with row 1
  { a: "x|y", b: "z" }, // a real duplicate of row 0
];
const separatorSafe = await new CompositeUniqueChecker({ fields: ["a", "b"] }).check(tricky);
show("2. separator-safe encoding (2 unique keys, 1 duplicate)", separatorSafe);

// --- 3. Same-looking values of different types stay apart -------------------
const typed = [{ v: 1 }, { v: "1" }, { v: true }, { v: null }, { v: 1 }];
const typedReport = await new CompositeUniqueChecker({ fields: ["v"] }).check(typed);
show("3. type-tagged keys: 1, \"1\", true, null are distinct", typedReport);

// --- 4. Configurable null equality ------------------------------------------
const withNulls = [{ id: null }, { id: null }, { id: 7 }, { id: 7 }];
const nullsMerge = await new CompositeUniqueChecker({ fields: ["id"] }).check(withNulls);
show("4a. nullsEqual: true (default)", nullsMerge);
const nullsDistinct = await new CompositeUniqueChecker({
  fields: ["id"],
  nullsEqual: false,
}).check(withNulls);
show("4b. nullsEqual: false", nullsDistinct);

// --- 5. Schema validation with instancePath + rulePath ----------------------
const schema: Schema = {
  type: "object",
  properties: {
    id: { type: "integer" },
    name: { type: "string" },
  },
  required: ["id", "name"],
  additionalProperties: false,
};
try {
  await new CompositeUniqueChecker({ fields: ["id"], schema }).check([
    { id: 1, name: "ada" },
    { id: "not-an-integer", name: "grace", extra: true },
  ]);
} catch (err) {
  if (err instanceof RecordValidationError) {
    show("5. validation error carries instancePath and rulePath", {
      recordIndex: err.recordIndex,
      errors: err.errors,
    });
  } else {
    throw err;
  }
}

// --- 6. Memory-bounded run with hash-partitioned spill ----------------------
const bigStream = (function* () {
  let state = 12345;
  const next = () => {
    state = (Math.imul(state, 1103515245) + 12345) & 0x7fffffff;
    return state;
  };
  for (let i = 0; i < 5000; i++) {
    yield { id: next() % 400, grp: `g${next() % 11}` };
  }
})();

const reference = await new CompositeUniqueChecker({ fields: ["id", "grp"] }).check(
  (function* () {
    let state = 12345;
    const next = () => {
      state = (Math.imul(state, 1103515245) + 12345) & 0x7fffffff;
      return state;
    };
    for (let i = 0; i < 5000; i++) yield { id: next() % 400, grp: `g${next() % 11}` };
  })(),
);

const spilled = await new CompositeUniqueChecker({
  fields: ["id", "grp"],
  maxInMemoryKeys: 64, // force spilling
  spillPartitions: 16,
}).check(bigStream);

show("6. spill run (maxInMemoryKeys=64, 5000 records)", {
  spilled: spilled.spilled,
  totalRecords: spilled.totalRecords,
  uniqueKeyCount: spilled.uniqueKeyCount,
  duplicateOccurrences: spilled.duplicateOccurrences,
  firstDuplicates: spilled.duplicates.slice(0, 3),
});
console.log(
  `spill result matches in-memory reference: ${
    JSON.stringify(spilled.duplicates) === JSON.stringify(reference.duplicates) &&
    spilled.uniqueKeyCount === reference.uniqueKeyCount
  }`,
);
