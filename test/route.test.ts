import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Route,
  DuplicateParamError,
  EmptyParamError,
  MissingParamError,
  UnknownParamError,
  RouteError,
} from "../src/index.ts";

test("parent and child paths compose into one effective route", () => {
  const parent = new Route("/orgs/:orgId");
  const child = parent.child("/members/:memberId");
  assert.equal(child.pattern, "/orgs/:orgId/members/:memberId");
  assert.deepEqual(child.paramNames, ["orgId", "memberId"]);
  assert.equal(
    child.build({ orgId: "acme", memberId: "42" }),
    "/orgs/acme/members/42",
  );
});

test("root parent composes without a double slash", () => {
  const root = new Route("/");
  const child = root.child("/users/:id");
  assert.equal(child.pattern, "/users/:id");
  assert.equal(child.build({ id: "7" }), "/users/7");
});

test("duplicate param names on the same effective path are rejected", () => {
  assert.throws(() => new Route("/a/:id/b/:id"), DuplicateParamError);
  const parent = new Route("/users/:id");
  assert.throws(() => parent.child("/posts/:id"), DuplicateParamError);
  assert.throws(() => parent.child("/posts/:id?"), DuplicateParamError);
});

test("same param name on different routes is allowed", () => {
  const a = new Route("/users/:id");
  const b = new Route("/posts/:id");
  assert.equal(a.build({ id: "1" }), "/users/1");
  assert.equal(b.build({ id: "2" }), "/posts/2");
});

test("omitted optional parent segment leaves child params independent", () => {
  const route = new Route("/:tenant?").child("/users/:id");
  const url = route.build({ id: "7" });
  assert.equal(url, "/users/7");
  const params = route.match(url);
  assert.deepEqual(params, { id: "7" });
  assert.equal(Object.hasOwn(params!, "tenant"), false);
});

test("present optional parent segment round-trips with child params", () => {
  const route = new Route("/:tenant?").child("/users/:id");
  const url = route.build({ tenant: "acme", id: "7" });
  assert.equal(url, "/acme/users/7");
  assert.deepEqual(route.match(url), { tenant: "acme", id: "7" });
});

test("optional parent directly followed by child param stays unambiguous", () => {
  const route = new Route("/:tenant?").child("/:id");
  assert.equal(route.build({ id: "7" }), "/7");
  assert.deepEqual(route.match("/7"), { id: "7" });
  assert.equal(route.build({ tenant: "t", id: "7" }), "/t/7");
  assert.deepEqual(route.match("/t/7"), { tenant: "t", id: "7" });
});

test("empty string parameter values are rejected", () => {
  const route = new Route("/users/:id");
  assert.throws(() => route.build({ id: "" }), EmptyParamError);
  const optional = new Route("/users/:id?");
  assert.throws(() => optional.build({ id: "" }), EmptyParamError);
});

test("missing required and unknown parameters are rejected", () => {
  const route = new Route("/users/:id");
  assert.throws(() => route.build({}), MissingParamError);
  assert.throws(
    () => route.build({ id: "1", extra: "x" } as never),
    UnknownParamError,
  );
});

test("reserved characters in values are encoded and decode back exactly", () => {
  const route = new Route("/files/:name");
  const tricky = "a/b?c#d%e f&g=h+i";
  const url = route.build({ name: tricky });
  assert.equal(url, "/files/" + encodeURIComponent(tricky));
  assert.equal(url.split("/").length, 3);
  assert.deepEqual(route.match(url), { name: tricky });
});

test("percent signs and pre-encoded-looking values round-trip literally", () => {
  const route = new Route("/files/:name");
  const url = route.build({ name: "%2F" });
  assert.equal(url, "/files/%252F");
  assert.deepEqual(route.match(url), { name: "%2F" });
  assert.deepEqual(route.match("/files/%2F"), { name: "/" });
});

test("unicode values round-trip", () => {
  const route = new Route("/users/:name");
  const url = route.build({ name: "中文/用户" });
  assert.deepEqual(route.match(url), { name: "中文/用户" });
});

test("trailing slash rules: build never emits one, match tolerates one", () => {
  const route = new Route("/users/:id");
  assert.equal(route.build({ id: "7" }), "/users/7");
  assert.deepEqual(route.match("/users/7/"), { id: "7" });
  assert.deepEqual(route.match("/users/7"), { id: "7" });
  const root = new Route("/");
  assert.equal(root.build({}), "/");
  assert.deepEqual(root.match("/"), {});
});

test("malformed paths do not match", () => {
  const route = new Route("/users/:id");
  assert.equal(route.match("//"), null);
  assert.equal(route.match("/users//7"), null);
  assert.equal(route.match("/users/7//"), null);
  assert.equal(route.match("users/7"), null);
  assert.equal(route.match("/users/7/extra"), null);
  assert.equal(route.match("/users"), null);
  assert.equal(route.match("/users/%E0%A4%A"), null);
  assert.equal(route.match("/users/7?x=1"), null);
});

test("invalid patterns are rejected at compile time", () => {
  assert.throws(() => new Route(""), RouteError);
  assert.throws(() => new Route("users"), RouteError);
  assert.throws(() => new Route("/users/"), RouteError);
  assert.throws(() => new Route("/users//:id"), RouteError);
  assert.throws(() => new Route("/users/:"), RouteError);
  assert.throws(() => new Route("/users/:1bad"), RouteError);
});

test("round-trip sweep: match(build(params)) returns the same params", () => {
  const cases: Array<[Route, Record<string, string>]> = [
    [new Route("/"), {}],
    [new Route("/a/b/c"), {}],
    [new Route("/users/:id"), { id: "7" }],
    [new Route("/users/:id?"), {}],
    [new Route("/users/:id?"), { id: "7" }],
    [new Route("/:tenant?/users/:id"), { id: "7" }],
    [new Route("/:tenant?/users/:id"), { tenant: "acme", id: "7" }],
    [new Route("/:a?/x/:b?"), { b: "5" }],
    [new Route("/:a?/x/:b?"), { a: "1" }],
    [new Route("/:a?/x/:b?"), { a: "1", b: "5" }],
    [new Route("/:a?/x/:b?"), {}],
    [
      new Route("/orgs/:orgId").child("/members/:memberId"),
      { orgId: "o/1", memberId: "m#2" },
    ],
    [new Route("/files/:name"), { name: "a b%c/d?e#f" }],
    [new Route("/files/:name"), { name: "用户 名" }],
    [new Route("/x/:v"), { v: "%2F" }],
    [new Route("/x/:v"), { v: "!" }],
  ];
  for (const [route, params] of cases) {
    const url = route.build(params as never);
    assert.ok(!url.endsWith("/") || url === "/", `no trailing slash: ${url}`);
    assert.deepEqual(route.match(url), params, `round-trip of ${url}`);
    assert.deepEqual(
      route.match(url === "/" ? url : url + "/"),
      params,
      `round-trip of ${url}/ (trailing slash tolerated)`,
    );
  }
});
