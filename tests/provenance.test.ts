import { test } from "node:test";
import assert from "node:assert/strict";
import { Engine } from "../src/index.ts";

test("every output path maps to its template path and input paths", () => {
  const eng = new Engine(
    {
      title: "Hello ${user.name}",
      age: "${user.age}",
      tags: { $for: "user.tags", as: "t", body: "${upper(@t)}" },
      literal: "no inputs here",
    },
    { user: { name: "Ada", age: 36, tags: ["a", "b"] } },
  );
  const p = eng.provenance;
  assert.equal(p["/"].template, "/");
  assert.equal(p["/title"].template, "/title");
  assert.deepEqual(p["/title"].inputs, ["/user/name"]);
  assert.deepEqual(p["/age"].inputs, ["/user/age"]);
  assert.equal(p["/tags"].template, "/tags");
  assert.deepEqual(p["/tags"].inputs, ["/user/tags"]);
  assert.equal(p["/tags/0"].template, "/tags/body");
  assert.deepEqual(p["/tags/0"].inputs, ["/user/tags/0"]);
  assert.deepEqual(p["/tags/1"].inputs, ["/user/tags/1"]);
  assert.deepEqual(p["/literal"].inputs, []);
});

test("missing reads are recorded with the attempted input path", () => {
  const eng = new Engine(
    { nick: { $get: "user.nick", missing: "null" } },
    { user: {} },
  );
  assert.deepEqual(eng.provenance["/nick"].inputs, ["/user/nick"]);
  assert.equal(eng.output.nick, null);
});

test("provenance after incremental update matches full re-render exactly", () => {
  const template = {
    users: {
      $for: "users",
      as: "u",
      body: { name: "${@u.name}", tags: { $for: "@u.tags", as: "t", body: "${@t}" } },
    },
    count: "${len(users)}",
  };
  const data = {
    users: [
      { name: "a", tags: ["x", "y"] },
      { name: "b", tags: ["z"] },
    ],
  };
  const eng = new Engine(template, data);
  eng
    .begin()
    .move("/users", 0, 1)
    .move("/users/1/tags", 0, 1)
    .insert("/users/0/tags", 1, "new")
    .set("/users/1/name", "A2")
    .commit();
  const full = eng.fullRender();
  assert.deepEqual(eng.output, full.output);
  assert.deepEqual(eng.provenance, full.provenance);
});
