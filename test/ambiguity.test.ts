import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Route,
  Router,
  AmbiguousPatternError,
} from "../src/index.ts";

test("adjacent optional params are rejected as ambiguous", () => {
  assert.throws(() => new Route("/:a?/:b?"), AmbiguousPatternError);
});

test("optionals separated only by params are rejected as ambiguous", () => {
  assert.throws(() => new Route("/:a?/:b/:c?"), AmbiguousPatternError);
});

test("ambiguity across a parent/child boundary is rejected at compose time", () => {
  const parent = new Route("/:a?");
  assert.throws(() => parent.child("/:b?"), AmbiguousPatternError);
  assert.throws(() => parent.child("/:b/:c?"), AmbiguousPatternError);
  assert.doesNotThrow(() => parent.child("/:b/c/:d?"));
});

test("optionals separated by a static anchor are accepted", () => {
  const route = new Route("/:a?/x/:b?");
  assert.deepEqual(route.match("/x"), {});
  assert.deepEqual(route.match("/1/x"), { a: "1" });
  assert.deepEqual(route.match("/x/5"), { b: "5" });
  assert.deepEqual(route.match("/1/x/5"), { a: "1", b: "5" });
  assert.equal(route.match("/1/2/x"), null);
});

test("static anchor wins over optional param during backtracking", () => {
  const route = new Route("/:a?/users");
  assert.deepEqual(route.match("/users"), {});
  assert.deepEqual(route.match("/t/users"), { a: "t" });
  const built = route.build({});
  assert.equal(built, "/users");
  assert.deepEqual(route.match(built), {});
});

test("optional param value equal to the static anchor still round-trips", () => {
  const route = new Route("/:a?/users");
  const url = route.build({ a: "users" });
  assert.equal(url, "/users/users");
  assert.deepEqual(route.match(url), { a: "users" });
});

test("router prefers static routes over param routes regardless of order", () => {
  for (const order of [0, 1] as const) {
    const router = new Router();
    const routes = [
      () => router.add("/users/new"),
      () => router.add("/users/:id"),
    ];
    const first = routes[order]!();
    const second = routes[1 - order]!();
    const staticRoute = order === 0 ? first : second;
    const paramRoute = order === 0 ? second : first;
    const staticMatch = router.match("/users/new");
    assert.equal(staticMatch!.route, staticRoute);
    assert.deepEqual(staticMatch!.params, {});
    const paramMatch = router.match("/users/7");
    assert.equal(paramMatch!.route, paramRoute);
    assert.deepEqual(paramMatch!.params, { id: "7" });
  }
});

test("router matches composed routes and reports the matched route", () => {
  const router = new Router();
  const parent = new Route("/orgs/:orgId");
  const members = router.add(parent.child("/members"));
  const member = router.add(parent.child("/members/:memberId"));
  assert.equal(router.match("/orgs/acme/members")!.route, members);
  const hit = router.match("/orgs/acme/members/42");
  assert.equal(hit!.route, member);
  assert.deepEqual(hit!.params, { orgId: "acme", memberId: "42" });
  assert.equal(router.match("/nope"), null);
});

test("router tolerates trailing slash on any route", () => {
  const router = new Router();
  router.add("/users/:id");
  const hit = router.match("/users/7/");
  assert.deepEqual(hit!.params, { id: "7" });
});
