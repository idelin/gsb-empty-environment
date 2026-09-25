import {
  aggregateRows,
  attachTree,
  filterRows,
  joinRows,
  loadTree,
  splitRows,
} from "./operations.ts";
import type { JsonValue } from "./types.ts";

const orders: JsonValue = {
  data: [
    {
      id: "o1",
      active: true,
      customerId: "c1",
      items: [{ sku: "a", amount: 10, qty: 2 }, { sku: "b", amount: 5, qty: 1 }],
    },
    {
      id: "o2",
      active: true,
      customerId: "c2",
      items: [{ sku: "a", amount: 20, qty: 1 }],
    },
    {
      id: "o3",
      active: false,
      customerId: "c1",
      items: [{ sku: "a", amount: 99, qty: 1 }],
    },
  ],
};

const customers: JsonValue = {
  data: [
    { id: "c1", name: "Alice" },
    { id: "c2", name: "Bob" },
    { id: "c1", name: "Alice duplicate account" },
  ],
};

const loaded = loadTree(orders, {
  source: "orders.json",
  version: "orders-v1",
  rowsPath: "/data",
});

const active = filterRows(loaded, {
  version: "active-orders-v1",
  predicate: (row) => Boolean(row && typeof row === "object" && "active" in row && row.active),
});

const split = splitRows(active, {
  version: "order-items-v1",
  field: "items",
  as: "item",
});

const customersRelation = attachTree(split.lineage, customers, {
  source: "customers.json",
  version: "customers-v1",
  rowsPath: "/data",
});

const joined = joinRows(split, customersRelation, {
  version: "item-customers-v1",
  leftKey: "customerId",
  rightKey: "id",
  leftAs: "order",
  rightAs: "customer",
});

const totals = aggregateRows(joined, {
  version: "sku-totals-v1",
  groupBy: "order/item/sku",
  keyAs: "sku",
  aggregates: [
    { as: "quantity", op: "sum", field: "order/item/qty" },
    { as: "matchedRows", op: "count" },
  ],
});

console.log("versions:");
console.log(totals.lineage.versionNames());
console.log("\nfinal rows:");
console.log(JSON.stringify(totals.rows, null, 2));

console.log("\nlineage of /0/quantity:");
console.log(JSON.stringify(totals.lineage.origins(totals.version, "/0/quantity"), null, 2));

console.log("\nparent chain for /0/quantity:");
console.log(JSON.stringify(totals.lineage.explain(totals.version, "/0/quantity"), null, 2));

console.log("\nfiltered source row /data/2 is absent from active output:");
console.log(
  active.lineage
    .entries(active.version)
    .flatMap((node) => node.origins)
    .some((origin) => origin.path.startsWith("/data/2")),
);
