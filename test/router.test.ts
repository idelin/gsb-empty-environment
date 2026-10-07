import { test } from "node:test";
import assert from "node:assert/strict";
import { Router } from "../src/index.ts";

test("matches literals, params and wildcards", () => {
  const router = new Router<string>([
    { path: "/users/list", data: "list" },
    { path: "/users/:id", data: "show" },
    { path: "/files/*path", data: "files" },
  ]);
  assert.equal(router.match("/users/list")?.data, "list");
  assert.equal(router.match("/users/42")?.data, "show");
  assert.deepEqual(router.match("/users/42")?.params, { id: "42" });
  assert.deepEqual(router.match("/files/a/b/c")?.params, { path: ["a", "b", "c"] });
  assert.deepEqual(router.match("/files")?.params, { path: [] });
  assert.equal(router.match("/nope"), null);
});

test("more literal segments win over params and wildcards", () => {
  const router = new Router<string>([
    { path: "/a/:x", data: "param" },
    { path: "/a/b", data: "literal" },
    { path: "/a/*rest", data: "wild" },
  ]);
  assert.equal(router.match("/a/b")?.data, "literal");
  assert.equal(router.match("/a/c")?.data, "param");
  assert.equal(router.match("/a/b/c")?.data, "wild");
});

test("fewer omitted optional groups win", () => {
  const router = new Router<string>([
    { path: "/a(/b)", data: "optional" },
    { path: "/a", data: "plain" },
  ]);
  assert.equal(router.match("/a")?.data, "plain");
  assert.equal(router.match("/a/b")?.data, "optional");
});

test("registration order breaks full ties", () => {
  const router = new Router<string>([
    { path: "/a/:x", data: "first" },
    { path: "/a/:y", data: "second" },
  ]);
  assert.equal(router.match("/a/1")?.data, "first");
  assert.deepEqual(
    router.matchAll("/a/1").map((m) => m.data),
    ["first", "second"],
  );
});

test("percent-decoding happens after splitting, so %2F never re-segments", () => {
  const router = new Router<string>([
    { path: "/files/:name", data: "one" },
    { path: "/files/*path", data: "many" },
  ]);
  const one = router.match("/files/a%2Fb");
  assert.equal(one?.data, "one");
  assert.deepEqual(one?.params, { name: "a/b" });

  const many = router.match("/files/a%2Fb/c%2Fd");
  assert.equal(many?.data, "many");
  assert.deepEqual(many?.params, { path: ["a/b", "c/d"] });
});

test("decodes percent-encoded values in params and literals", () => {
  const router = new Router<string>([
    { path: "/hello world/:name", data: "greet" },
  ]);
  const match = router.match("/hello%20world/%E4%BD%A0%E5%A5%BD");
  assert.equal(match?.data, "greet");
  assert.deepEqual(match?.params, { name: "你好" });
});

test("rejects malformed paths", () => {
  const router = new Router<string>([{ path: "/a/:x", data: "a" }]);
  assert.equal(router.match("a/b"), null);
  assert.equal(router.match("/a//b"), null);
  assert.equal(router.match("/a/%"), null);
  assert.equal(router.match("/a/%E0%A4%A"), null);
});

test("optional groups with params capture values when present", () => {
  const router = new Router<string>([{ path: "/users(/:id)/edit", data: "edit" }]);
  assert.deepEqual(router.match("/users/edit")?.params, {});
  assert.deepEqual(router.match("/users/7/edit")?.params, { id: "7" });
  assert.equal(router.match("/users"), null);
});
