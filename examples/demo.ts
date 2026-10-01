import { TemplateEngine } from "../src/engine.ts";
import type { JsonValue } from "../src/json.ts";

const template: JsonValue = {
  title: "${app.title | upper}",
  note: "literal \\${app.title} stays as-is",
  users: {
    $for: "app.users",
    $as: "u",
    $index: "i",
    $body: {
      rank: "${i}",
      name: "${u.name}",
      tag: "${u.profile.tag | default('none')}", // missing -> 'none', explicit null -> null
      admin: { $if: "u.admin", $then: "yes", $else: "no" },
      hobbies: { $for: "u.hobbies", $as: "h", $body: "${h}" },
    },
  },
};

const data: JsonValue = {
  app: {
    title: "team roster",
    users: [
      { name: "ann", admin: true, hobbies: ["ski", "go"], profile: { tag: "lead" } },
      { name: "bob", admin: false, hobbies: ["chess"], profile: {} },
      { name: "cid", admin: null, hobbies: [], profile: { tag: null } },
    ],
  },
};

const engine = new TemplateEngine(template);

console.log("=== 1. initial render (full) ===");
const r1 = engine.setData(data);
console.log(JSON.stringify(r1.output, null, 2));

console.log("\n=== 2. transaction: move ann to the end, rename her, add a hobby ===");
const r2 = engine.transaction((tx) => {
  tx.move("app.users", 0, 2); // entity id follows ann
  tx.set("app.users.2.name", "ann (boss)");
  tx.insert("app.users.2.hobbies", 2, "chess");
});
console.log("user names:", JSON.stringify((r2.output as any).users.map((u: any) => u.name)));
console.log("ann's hobbies:", JSON.stringify((r2.output as any).users[2].hobbies));
console.log("render stats (cache):", engine.stats);

console.log("\n=== 3. incremental output matches a full re-render ===");
const full = engine.fullRender();
console.log("output equal:", JSON.stringify(r2.output) === JSON.stringify(full.output));
console.log("provenance equal:", JSON.stringify(r2.provenance) === JSON.stringify(full.provenance));

console.log("\n=== 4. dual provenance sample (output -> template + input) ===");
for (const p of r2.provenance.filter((p) => p.out.includes("name") || p.out === "/title")) {
  console.log(`  ${p.out}  <-  template ${p.template}  <-  input ${JSON.stringify(p.input)}`);
}

console.log("\n=== 5. failed transaction rolls everything back ===");
try {
  engine.transaction((tx) => {
    tx.set("app.users.0.name", "should not survive");
    tx.set("app.title", 123); // upper(number) -> whitelisted function failure
  });
} catch (err) {
  console.log("rolled back:", (err as Error).message);
}
console.log("title still:", (engine.output as any).title);
console.log("data intact:", JSON.stringify((engine.getData() as any).app.users[0].name));
