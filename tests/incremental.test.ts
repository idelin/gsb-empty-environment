import { test } from "node:test";
import assert from "node:assert/strict";
import { Engine, type Fragment } from "../src/index.ts";

function loopChildren(eng: Engine, key: string): Fragment[] {
  const root = eng.rootFragment.children as Map<string, Fragment>;
  return (root.get(key) as Fragment).children as Fragment[];
}

test("leaf set re-renders only the affected fragment", () => {
  const eng = new Engine(
    {
      a: "${x}",
      b: { $for: "items", as: "it", body: "${upper(@it)}" },
    },
    { x: 1, items: ["p", "q"] },
  );
  const before = eng.rootFragment.children as Map<string, Fragment>;
  const aBefore = before.get("a");
  const bBefore = before.get("b");
  eng.begin().set("/x", 42).commit();
  const after = eng.rootFragment.children as Map<string, Fragment>;
  assert.equal(eng.output.a, 42);
  assert.notEqual(after.get("a"), aBefore); // re-rendered
  assert.equal(after.get("b"), bBefore); // untouched subtree reused by identity
  assert.ok(eng.verify());
});

test("array reorder reuses fragments by entity id (not content)", () => {
  const eng = new Engine(
    { users: { $for: "users", as: "u", body: { name: "${@u.name}" } } },
    { users: [{ name: "x" }, { name: "y" }, { name: "y" }] }, // duplicate content
  );
  const before = loopChildren(eng, "users").slice();
  eng.begin().move("/users", 0, 2).commit();
  const after = loopChildren(eng, "users");
  assert.deepEqual(eng.output.users, [{ name: "y" }, { name: "y" }, { name: "x" }]);
  // all three fragments reused, reordered
  assert.deepEqual(
    after.map((f) => before.indexOf(f)),
    [1, 2, 0],
  );
  // the two identical {name:'y'} entities stay distinct: moving them swaps identities
  eng.begin().move("/users", 0, 1).commit();
  const swapped = loopChildren(eng, "users");
  assert.equal(swapped[0], before[2]);
  assert.equal(swapped[1], before[1]);
  assert.ok(eng.verify());
});

test("variable-length arrays: insert and remove", () => {
  const eng = new Engine(
    { list: { $for: "items", as: "it", body: "${@it}" }, n: "${len(items)}" },
    { items: [1, 2, 3] },
  );
  const before = loopChildren(eng, "list").slice();
  eng.begin().insert("/items", 1, 99).commit();
  assert.deepEqual(eng.output.list, [1, 99, 2, 3]);
  assert.equal(eng.output.n, 4);
  let after = loopChildren(eng, "list");
  assert.equal(after[0], before[0]);
  assert.equal(after[2], before[1]);
  assert.equal(after[3], before[2]);
  assert.ok(eng.verify());

  eng.begin().remove("/items/0").commit();
  assert.deepEqual(eng.output.list, [99, 2, 3]);
  after = loopChildren(eng, "list");
  assert.equal(after[1], before[1]);
  assert.ok(eng.verify());
});

test("nested moves: inner array reorder plus outer reorder", () => {
  const template = {
    groups: {
      $for: "groups",
      as: "g",
      body: {
        label: "${@g.label}",
        members: { $for: "@g.members", as: "m", body: "${@m}" },
      },
    },
  };
  const data = {
    groups: [
      { label: "g1", members: ["a", "b", "c"] },
      { label: "g2", members: ["d", "e"] },
    ],
  };
  const eng = new Engine(template, data);
  const tx = eng.begin();
  tx.move("/groups/0/members", 0, 2); // inner reorder
  tx.move("/groups", 0, 1); // outer reorder
  tx.set("/groups/1/label", "G2!");
  tx.commit();
  assert.deepEqual(eng.output.groups, [
    { label: "g2", members: ["d", "e"] },
    { label: "G2!", members: ["b", "c", "a"] },
  ]);
  assert.ok(eng.verify());
});

test("missing values appear and disappear across commits", () => {
  const eng = new Engine(
    {
      nick: { $get: "user.nick", missing: "empty" },
      city: { $get: "user.addr.city", missing: "omit" },
    },
    { user: {} },
  );
  assert.deepEqual(eng.output, { nick: "" });
  eng.begin().set("/user/nick", "ada").set("/user/addr", { city: "Paris" }).commit();
  assert.deepEqual(eng.output, { nick: "ada", city: "Paris" });
  assert.ok(eng.verify());
  eng.begin().remove("/user/addr").remove("/user/nick").commit();
  assert.deepEqual(eng.output, { nick: "" });
  assert.ok(eng.verify());
});

test("whole-subtree replacement and conditional flips", () => {
  const eng = new Engine(
    {
      profile: "${user.profile}",
      banner: { $if: "user.vip", then: "VIP", else: "basic" },
    },
    { user: { profile: { age: 1 }, vip: false } },
  );
  eng.begin().set("/user/profile", { age: 2, extra: true }).set("/user/vip", true).commit();
  assert.deepEqual(eng.output.profile, { age: 2, extra: true });
  assert.equal(eng.output.banner, "VIP");
  assert.ok(eng.verify());
});

test("provenance follows entities across reorder", () => {
  const eng = new Engine(
    { users: { $for: "users", as: "u", body: "${@u.name}" } },
    { users: [{ name: "a" }, { name: "b" }, { name: "c" }] },
  );
  const before = loopChildren(eng, "users").slice();
  assert.deepEqual(eng.provenance["/users/0"].inputs, ["/users/0/name"]);
  eng.begin().move("/users", 0, 2).commit();
  const after = loopChildren(eng, "users");
  // entity "a" kept its identity (same fragment) and moved to output position 2
  assert.equal(after[2], before[0]);
  assert.equal(after[0], before[1]);
  // provenance paths are positional: they now resolve to the entity's new location
  assert.deepEqual(eng.provenance["/users/2"].inputs, ["/users/2/name"]);
  assert.deepEqual(eng.output.users, ["b", "c", "a"]);
  // and the incrementally maintained table matches a full re-render exactly
  assert.deepEqual(eng.provenance, eng.fullRender().provenance);
  assert.ok(eng.verify());
});

test("multiple sequential transactions stay consistent", () => {
  const eng = new Engine(
    { items: { $for: "items", as: "i", body: "${add(@i, 1)}" } },
    { items: [1, 2, 3] },
  );
  for (let k = 0; k < 10; k++) {
    const tx = eng.begin();
    tx.set(`/items/${k % 3}`, k).insert("/items", 0, k).move("/items", 1, 2).commit();
    assert.ok(eng.verify(), `verify failed after round ${k}`);
  }
});
