import { test } from "node:test";
import assert from "node:assert/strict";
import { TemplateEngine } from "../src/engine.ts";
import type { JsonValue } from "../src/json.ts";
import { USERS_TEMPLATE, usersData } from "./helpers.ts";

function userNames(output: JsonValue): string[] {
  return (output as { users: Array<{ name: string }> }).users.map((u) => u.name);
}

test("reorder keeps entity identity: all fragments reused, output matches full render", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  const before = eng.entityIds("app.users");

  const r = eng.transaction((tx) => tx.move("app.users", 0, 2));

  assert.deepStrictEqual(userNames(r.output), ["bob", "cid", "ann"]);
  // entity ids follow the moved entries: [ann, bob, cid] -> [bob, cid, ann]
  assert.deepStrictEqual(eng.entityIds("app.users"), [before[1], before[2], before[0]]);
  // every loop-iteration fragment was reused (3 users; hobby loops ride along)
  assert.strictEqual(eng.stats.misses, 0);
  assert.strictEqual(eng.stats.hits, 3);
  // incremental result equals a full re-render, output AND provenance
  const full = eng.fullRender();
  assert.deepStrictEqual(r.output, full.output);
  assert.deepStrictEqual(r.provenance, full.provenance);
});

test("identical content does not mean identical identity", () => {
  const tpl: JsonValue = { list: { $for: "items", $as: "it", $body: { v: "${it.v}" } } };
  const eng = new TemplateEngine(tpl);
  eng.setData({ items: [{ v: "same" }, { v: "same" }, { v: "same" }] });

  const ids = eng.entityIds("items");
  assert.strictEqual(new Set(ids).size, 3, "same-content nodes get distinct entity ids");

  eng.transaction((tx) => tx.move("items", 0, 2));
  assert.deepStrictEqual(eng.entityIds("items"), [ids[1], ids[2], ids[0]]);
  assert.strictEqual(eng.stats.hits, 3); // all three entities tracked through the move

  // mutate the entity now at index 0 (originally index 1) and verify attribution
  const r = eng.transaction((tx) => tx.set("items.0.v", "changed"));
  assert.deepStrictEqual(r.output, { list: [{ v: "changed" }, { v: "same" }, { v: "same" }] });
  const prov = new Map(r.provenance.map((p) => [p.out, p]));
  assert.deepStrictEqual(prov.get("/list/0/v")?.input, ["/items/0/v"]);
  assert.deepStrictEqual(prov.get("/list/2/v")?.input, ["/items/2/v"]);
  assert.deepStrictEqual(r.output, eng.fullRender().output);
  assert.deepStrictEqual(r.provenance, eng.fullRender().provenance);
});

test("nested move: inner reorder reuses inner fragments, matches full render", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());

  const r = eng.transaction((tx) => tx.move("app.users.0.hobbies", 0, 1));

  const users = (r.output as { users: Array<{ hobbies: string[] }> }).users;
  assert.deepStrictEqual(users[0].hobbies, ["b", "a"]);
  // ann's fragment re-rendered (her hobbies array changed); the two hobby
  // fragments and the untouched users bob/cid were reused.
  assert.strictEqual(eng.stats.misses, 1);
  assert.strictEqual(eng.stats.hits, 4);
  const full = eng.fullRender();
  assert.deepStrictEqual(r.output, full.output);
  assert.deepStrictEqual(r.provenance, full.provenance);
});

test("variable-length arrays: insert and remove only touch affected fragments", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());

  const r = eng.transaction((tx) => {
    tx.insert("app.users", 1, { name: "new", admin: true, hobbies: ["z"], profile: { tag: "t" } });
    tx.remove("app.users", 0);
  });

  assert.deepStrictEqual(userNames(r.output), ["new", "bob", "cid"]);
  // misses: the inserted user AND its inner hobby fragment; hits: bob and cid
  assert.strictEqual(eng.stats.misses, 2);
  assert.strictEqual(eng.stats.hits, 2);
  const full = eng.fullRender();
  assert.deepStrictEqual(r.output, full.output);
  assert.deepStrictEqual(r.provenance, full.provenance);
});

test("missing values appear and disappear across transactions", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  const r0 = eng.setData(usersData());
  const users0 = (r0.output as { users: Array<{ tag: JsonValue }> }).users;
  assert.strictEqual(users0[1].tag, "none"); // bob has no profile.tag -> default

  const r1 = eng.transaction((tx) => tx.set("app.users.1.profile.tag", "zzz"));
  const users1 = (r1.output as { users: Array<{ tag: JsonValue }> }).users;
  assert.strictEqual(users1[1].tag, "zzz");
  assert.deepStrictEqual(r1.output, eng.fullRender().output);

  const r2 = eng.transaction((tx) => tx.set("app.users.1.profile", {}));
  const users2 = (r2.output as { users: Array<{ tag: JsonValue }> }).users;
  assert.strictEqual(users2[1].tag, "none"); // missing again -> default
  const full = eng.fullRender();
  assert.deepStrictEqual(r2.output, full.output);
  assert.deepStrictEqual(r2.provenance, full.provenance);
});

test("conditional toggles incrementally and stays consistent", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  const r = eng.transaction((tx) => tx.set("app.users.1.admin", true));
  const users = (r.output as { users: Array<{ admin: string }> }).users;
  assert.strictEqual(users[1].admin, "yes");
  assert.deepStrictEqual(r.output, eng.fullRender().output);
});

test("provenance tracks entities through reorder", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  eng.setData(usersData());
  const r = eng.transaction((tx) => tx.move("app.users", 0, 2));
  const prov = new Map(r.provenance.map((p) => [p.out, p]));
  // ann moved to input index 2; her output row is index 2 as well
  assert.deepStrictEqual(prov.get("/users/2/name")?.input, ["/app/users/2/name"]);
  assert.strictEqual(prov.get("/users/2/name")?.template, "/users/$body/name");
  assert.deepStrictEqual(prov.get("/users/0/hobbies/0")?.input, ["/app/users/0/hobbies/0"]);
});

test("a mixed edit sequence always matches a full re-render", () => {
  const eng = new TemplateEngine(USERS_TEMPLATE);
  let result = eng.setData(usersData());
  const edits: Array<(tx: {
    set(p: string, v: JsonValue): void;
    insert(p: string, i: number, v: JsonValue): void;
    remove(p: string, i: number): void;
    move(p: string, f: number, t: number): void;
  }) => void> = [
    (tx) => tx.move("app.users", 2, 0), // [cid, ann, bob]
    (tx) => tx.set("app.users.0.name", "CID"),
    (tx) => {
      tx.insert("app.users.0.hobbies", 0, "mid");
      tx.insert("app.users.0.hobbies", 1, "end");
    },
    (tx) => tx.move("app.users.0.hobbies", 0, 1), // ["end", "mid"]
    (tx) => tx.remove("app.users", 1), // drop ann -> [cid, bob]
    (tx) => tx.set("app.title", "changed"),
    (tx) => tx.insert("app.users", 0, { name: "top", admin: false, hobbies: [], profile: {} }),
    (tx) => tx.set("app.users.1.profile", { tag: "back" }),
    (tx) => tx.move("app.users", 0, 2), // [cid, bob, top]
  ];
  for (const edit of edits) {
    result = eng.transaction(edit);
    const full = eng.fullRender();
    assert.deepStrictEqual(result.output, full.output, "output diverged from full render");
    assert.deepStrictEqual(result.provenance, full.provenance, "provenance diverged");
  }
  assert.deepStrictEqual(userNames(result.output), ["CID", "bob", "top"]);
  assert.strictEqual((result.output as { title: string }).title, "CHANGED");
  const users = (result.output as { users: Array<{ tag: JsonValue; hobbies: string[] }> }).users;
  assert.strictEqual(users[0].tag, "back");
  assert.deepStrictEqual(users[0].hobbies, ["end", "mid"]);
});
