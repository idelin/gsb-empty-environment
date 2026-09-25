import assert from "node:assert/strict";
import { describe, it } from "node:test";
import {
  aggregateRows,
  attachTree,
  filterRows,
  joinRows,
  loadTree,
  splitRows,
} from "./operations.ts";
import { LineageError, type JsonValue } from "./types.ts";

const orders: JsonValue = {
  data: [
    {
      id: "o1",
      keep: true,
      customerId: "c1",
      items: [{ sku: "a", qty: 2, note: "first" }, { sku: "b", qty: 1 }],
    },
    {
      id: "o2",
      keep: true,
      customerId: "c1",
      items: [{ sku: "a", qty: 3, note: "second" }],
    },
    { id: "o3", keep: false, customerId: "x", items: [{ sku: "a", qty: 9 }] },
  ],
};

const customers: JsonValue = {
  data: [
    { id: "c1", name: "Alice", region: "APAC" },
    { id: "c1", name: "Alice again", region: "EMEA" },
  ],
};

function prepare() {
  const source = loadTree(orders, {
    source: "orders.json",
    version: "orders",
    rowsPath: "/data",
  });
  const filtered = filterRows(source, {
    version: "filtered",
    predicate: (row) => typeof row === "object" && row !== null && "keep" in row && row.keep === true,
  });
  const split = splitRows(filtered, {
    version: "split",
    field: "items",
    as: "item",
  });
  const customersRelation = attachTree(split.lineage, customers, {
    source: "customers.json",
    version: "customers",
    rowsPath: "/data",
  });
  const joined = joinRows(split, customersRelation, {
    version: "joined",
    leftKey: "customerId",
    rightKey: "id",
    leftAs: "summary",
    rightAs: "customer",
  });
  const totals = aggregateRows(joined, {
    version: "totals",
    groupBy: "summary/item/sku",
    keyAs: "sku",
    aggregates: [
      { as: "qty", op: "sum", field: "summary/item/qty" },
      { as: "count", op: "count" },
    ],
  });
  return { source, filtered, split, totals, customersRelation, joined };
}

describe("versioned field lineage", () => {
  it("keeps source paths under nested array indexes after filter and split", () => {
    const { split } = prepare();
    assert.deepEqual(split.rows.length, 3);
    assert.deepEqual(
      split.lineage.origins("split", "/0/item/qty"),
      [{ source: "orders.json", version: "orders", path: "/data/0/items/0/qty" }],
    );
    assert.deepEqual(
      split.lineage.origins("split", "/2/item/qty"),
      [{ source: "orders.json", version: "orders", path: "/data/1/items/0/qty" }],
    );
  });

  it("does not include filtered rows in any reachable lineage entry", () => {
    const { filtered } = prepare();
    const paths = filtered.lineage.entries("filtered").flatMap((node) =>
      node.origins.map((origin) => origin.path),
    );
    assert.ok(!paths.includes("/data/2"));
    assert.ok(!paths.some((path) => path.startsWith("/data/2/")));
    assert.deepEqual(filtered.rows.map((row) => row.id), ["o1", "o2"]);
  });

  it("aggregates an output field to every contributing source field", () => {
    const { totals } = prepare();
    const qtyRow = totals.rows.find((row) => row.sku === "a")!;
    assert.deepEqual(qtyRow.qty, 10);
    assert.deepEqual(totals.lineage.origins("totals", "/0/qty"), [
      { source: "orders.json", version: "orders", path: "/data/0/items/0/qty" },
      { source: "orders.json", version: "orders", path: "/data/1/items/0/qty" },
    ]);
    assert.deepEqual(totals.lineage.origins("totals", "/0/count"), [
      { source: "orders.json", version: "orders", path: "/data/0" },
      { source: "customers.json", version: "customers", path: "/data/0" },
      { source: "customers.json", version: "customers", path: "/data/1" },
      { source: "orders.json", version: "orders", path: "/data/1" },
    ]);
  });

  it("joins many-to-many and preserves both left and right sources", () => {
    const { joined } = prepare();
    assert.deepEqual(joined.rows.length, 6);
    assert.deepEqual(
      joined.lineage.origins("joined", "/0/customer/name"),
      [{ source: "customers.json", version: "customers", path: "/data/0/name" }],
    );
    assert.deepEqual(
      joined.lineage.origins("joined", "/1/customer/name"),
      [{ source: "customers.json", version: "customers", path: "/data/1/name" }],
    );
    assert.deepEqual(
      joined.lineage.origins("joined", "/1/summary/item/qty"),
      [{ source: "orders.json", version: "orders", path: "/data/0/items/0/qty" }],
    );
    assert.deepEqual(
      joined.lineage.origins("joined", "/4/summary/item/qty"),
      [{ source: "orders.json", version: "orders", path: "/data/1/items/0/qty" }],
    );
  });

  it("deduplicates exact source references but never merges equal values from different paths", () => {
    const { totals } = prepare();
    assert.deepEqual(totals.lineage.origins("totals", "/0/qty").length, 2);
    assert.deepEqual(totals.lineage.parents("totals", "/0/qty"), [
      { version: "joined", path: "/0/summary/item/qty" },
      { version: "joined", path: "/1/summary/item/qty" },
      { version: "joined", path: "/4/summary/item/qty" },
      { version: "joined", path: "/5/summary/item/qty" },
    ]);
  });

  it("keeps split item evidence separate from a retained field with the same value", () => {
    const tree: JsonValue = {
      data: [
        { id: "r1", picked: "same", choices: [{ name: "same" }, { name: "other" }] },
      ],
    };
    const loaded = loadTree(tree, { source: "split.json", version: "v", rowsPath: "/data" });
    const split = splitRows(loaded, { version: "s", field: "choices", as: "choice" });
    assert.deepEqual(split.rows[0]?.choice.name, "same");
    assert.deepEqual(split.lineage.origins("s", "/0/choice/name"), [
      { source: "split.json", version: "v", path: "/data/0/choices/0/name" },
    ]);
  });

  it("gives an empty filtered relation a root with no source evidence", () => {
    const loaded = loadTree(orders, { source: "orders.json", version: "v", rowsPath: "/data" });
    const empty = filterRows(loaded, { version: "empty", predicate: () => false });
    assert.deepEqual(empty.rows, []);
    assert.deepEqual(empty.lineage.origins("empty", "/"), []);
  });

  it("maps every collected aggregate array element to its contributing field", () => {
    const tree: JsonValue = {
      data: [
        { group: "g", value: { name: "one" } },
        { group: "g", value: { name: "two" } },
      ],
    };
    const loaded = loadTree(tree, { source: "collect.json", version: "v", rowsPath: "/data" });
    const collected = aggregateRows(loaded, {
      version: "c",
      groupBy: "group",
      aggregates: [{ as: "names", op: "collect", field: "value" }],
    });
    assert.deepEqual(collected.rows[0]?.names, [{ name: "one" }, { name: "two" }]);
    assert.deepEqual(collected.lineage.origins("c", "/0/names/1/name"), [
      { source: "collect.json", version: "v", path: "/data/1/value/name" },
    ]);
  });

  it("supports querying by version and composing direct parent evidence", () => {
    const { joined, totals } = prepare();
    assert.deepEqual(totals.lineage.versionNames(), [
      "orders",
      "filtered",
      "split",
      "customers",
      "joined",
      "totals",
    ]);
    assert.deepEqual(totals.lineage.parents("totals", "/0/qty").length, 4);
    const explained = totals.lineage.explain("totals", "/0/qty");
    assert.deepEqual(explained.operation, "aggregate");
    assert.deepEqual(explained.parents[0]?.operation, "join");
    assert.deepEqual(explained.parents[0]?.parents[0]?.operation, "split");
    assert.deepEqual(explained.origins.length, 2);
    assert.ok(joined.rows.length > 0);
  });

  it("handles escaped JSON pointer object keys", () => {
    const tree: JsonValue = { data: [{ "a/b": { "c~d": 7 } }] };
    const loaded = loadTree(tree, {
      source: "escaped.json",
      version: "source",
      rowsPath: "/data",
    });
    assert.deepEqual(
      loaded.lineage.origins("source", "/0/a~1b/c~0d"),
      [{ source: "escaped.json", version: "source", path: "/data/0/a~1b/c~0d" }],
    );
  });

  it("locates invalid input and callback errors at concrete paths", () => {
    assert.throws(
      () => loadTree({ data: [Number.NaN] }, { source: "bad", version: "v1", rowsPath: "/data" }),
      (error: unknown) => {
        assert.ok(error instanceof LineageError);
        assert.equal(error.code, "INVALID_JSON");
        assert.equal(error.path, "/data/0");
        assert.equal(error.version, "v1");
        return true;
      },
    );

    const loaded = loadTree(orders, {
      source: "orders.json",
      version: "orders",
      rowsPath: "/data",
    });
    assert.throws(
      () =>
        filterRows(loaded, {
          version: "bad-filter",
          predicate: (_row, context) => context.at("/missing") === true,
        }),
      (error: unknown) => {
        assert.ok(error instanceof LineageError);
        assert.equal(error.code, "CALLBACK_ERROR");
        assert.equal(error.path, "/0");
        assert.ok(error.cause instanceof LineageError);
        assert.equal(error.cause.code, "PATH_NOT_FOUND");
        return true;
      },
    );
  });

  it("locates a missing source rows path with source and version", () => {
    assert.throws(
      () => loadTree({ data: [] }, { source: "missing.json", version: "v", rowsPath: "/rows" }),
      (error: unknown) => {
        assert.ok(error instanceof LineageError);
        assert.equal(error.code, "PATH_NOT_FOUND");
        assert.equal(error.path, "/rows");
        assert.equal(error.version, "v");
        assert.deepEqual(error.locations, [{ source: "missing.json", version: "v", path: "/rows" }]);
        return true;
      },
    );
  });

  it("locates aggregate type errors at the contributing field", () => {
    const tree: JsonValue = {
      data: [{ group: "g", value: 1 }, { group: "g", value: "bad" }],
    };
    const loaded = loadTree(tree, { source: "mixed", version: "v", rowsPath: "/data" });
    assert.throws(
      () =>
        aggregateRows(loaded, {
          version: "agg",
          groupBy: "group",
          aggregates: [{ as: "total", op: "sum", field: "value" }],
        }),
      (error: unknown) => {
        assert.ok(error instanceof LineageError);
        assert.equal(error.code, "INVALID_VALUE");
        assert.match(error.message, /sum requires numeric values/);
        return true;
      },
    );
  });
});
