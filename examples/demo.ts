import { Engine, type JsonValue } from "../src/index.ts";

const template: JsonValue = {
  title: "Report for ${org.name}",
  literal: "escaped: \\${not.evaluated}",
  users: {
    $for: "org.users",
    as: "u",
    body: {
      name: "${upper(@u.name)}",
      nick: { $get: "@u.nick", missing: "null" },
      tags: { $for: "@u.tags", as: "t", body: "${@t}" },
      score: "${div(@u.points, @u.games)}",
    },
  },
  summary: "${fmt('{0} users, {1} total points', len(org.users), add(org.users[0].points, org.users[1].points))}",
};

const data: JsonValue = {
  org: {
    name: "Acme",
    users: [
      { name: "ada", nick: null, tags: ["admin", "dev"], points: 10, games: 2 },
      { name: "bob", tags: ["dev"], points: 6, games: 3 },
      { name: "cy", nick: "c", tags: [], points: 4, games: 4 },
    ],
  },
};

const eng = new Engine(template, data, { onMissing: "error", maxExpansions: 1000 });

const show = (label: string, v: unknown) => {
  console.log(`\n=== ${label} ===`);
  console.log(JSON.stringify(v, null, 2));
};

show("1. initial output (note: explicit null nick vs missing nick)", eng.output);
show("2. dual provenance: output path -> template path + input paths", {
  "/users/0/name": eng.provenance["/users/0/name"],
  "/users/1/nick": eng.provenance["/users/1/nick"],
  "/summary": eng.provenance["/summary"],
});

// --- incremental transaction: nested moves + variable length + missing toggle ---
const usersFrag = () =>
  (eng.rootFragment.children as Map<string, { children: unknown[] }>).get("users")!;
const before = (usersFrag().children as unknown[]).slice();

eng
  .begin()
  .move("/org/users", 0, 2) // reorder entities (stable ids track them)
  .move("/org/users/2/tags", 0, 1) // nested reorder
  .insert("/org/users", 1, { name: "dan", tags: ["ops"], points: 8, games: 4 }) // variable length
  .set("/org/users/0/nick", "bobby") // missing -> present
  .commit();

show("3. output after one transaction (move x2 + insert + set)", eng.output);
const after = usersFrag().children as unknown[];
console.log(
  `\nfragments reused by entity id: ${after.filter((f) => before.includes(f)).length} of ${after.length}`,
);
console.log("incremental output == full re-render:", eng.verify());
show("4. provenance after reorder (follows entities to new paths)", {
  "/users/3/name": eng.provenance["/users/3/name"],
  "/users/0/nick": eng.provenance["/users/0/nick"],
});

// --- failing transaction: function error rolls everything back ---
const dataBefore = JSON.stringify(eng.store.toJson());
try {
  eng
    .begin()
    .set("/org/users/0/games", 0) // div by zero in ${div(@u.points, @u.games)}
    .remove("/org/users/1")
    .commit();
} catch (e) {
  console.log(`\n=== 5. transaction failed as expected ===\n${(e as Error).message}`);
  console.log("rolled back:", JSON.stringify(eng.store.toJson()) === dataBefore);
  console.log("output untouched:", eng.verify());
}
