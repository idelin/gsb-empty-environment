import test from "node:test";
import assert from "node:assert/strict";
import {
  BudgetExceededError,
  collectStream,
  parseQuery,
  walkEvents,
  type JsonValue,
} from "../src/index.ts";
import { store } from "./helpers.ts";

test("streaming enforces the expand budget", async () => {
  await assert.rejects(
    collectStream(walkEvents(store), parseQuery("$..*"), { budget: { expand: 3 } }),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "expand",
  );
});

test("streaming enforces the compare budget inside staged filters", async () => {
  await assert.rejects(
    collectStream(walkEvents(store), parseQuery("$..book[?(@.price < 10)]"), {
      budget: { compare: 2 },
    }),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "compare",
  );
});

test("budget covers staged negative-index evaluation", async () => {
  const doc: JsonValue = { arr: Array.from({ length: 50 }, (_, i) => i) };
  await assert.rejects(
    collectStream(walkEvents(doc), parseQuery("$.arr[-1]"), { budget: { expand: 20 } }),
    (e: unknown) => e instanceof BudgetExceededError && e.kind === "expand",
  );
  const ok = await collectStream(walkEvents(doc), parseQuery("$.arr[-1]"), {
    budget: { expand: 1000 },
  });
  assert.deepEqual(ok.map((r) => r.value), [49]);
});
