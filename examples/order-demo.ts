import { ColumnarProjector, flattenJson } from "../src/index.ts";
import { buildOrder, buildOrderRaw } from "./sample-orders.ts";

const cities = ["Hangzhou", "Shanghai", "Ningbo"];
const rawOrders = cities.map((city, i) => buildOrderRaw(i + 1, city));

console.log("=== 1. Inferred virtual columns (schema of first order) ===");
const projector = new ColumnarProjector();
const schema = projector.inferPaths(buildOrder(1, cities[0]));
console.log(schema.join("\n"));

console.log("\n=== 2. Selective ingestion: only 'user.address.city' ===");
let totalMaterialized = 0;
let totalSkipped = 0;
for (const raw of rawOrders) {
  const stats = projector.addRowSelective(raw, ["user.address.city"]);
  totalMaterialized += stats.materializedValues;
  totalSkipped += stats.skippedValues;
}
console.log(
  `rows=${projector.size()} materializedValues=${totalMaterialized} ` +
    `skippedValues=${totalSkipped}`,
);

console.log("\n=== 3. Query only the city column ===");
const { columns, rowCount } = projector.query(["user.address.city"]);
console.log(`rowCount=${rowCount}`);
console.log("user.address.city ->", columns["user.address.city"]);

console.log("\n=== 4. Cost comparison vs full JSON.parse ===");
const fullLeafCount = flattenJson(JSON.parse(rawOrders[0])).size;
console.log(`full parse leaf values per order : ${fullLeafCount}`);
console.log(`selective materialized per order : ${totalMaterialized / rawOrders.length}`);
console.log(
  "=> items[], payment, logistics subtrees were skipped by raw scan, " +
    "never deserialized.",
);
