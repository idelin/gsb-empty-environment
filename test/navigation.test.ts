import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Router,
  type Guard,
  type GuardReturn,
  type NavigationResult,
  type Route,
} from "../src/navigation.ts";

/** A guard whose resolution is driven manually by the test. */
interface PendingCall {
  to: string;
  from: string;
  resolve: (value: GuardReturn) => void;
}

function controllableGuard() {
  const calls: PendingCall[] = [];
  const guard: Guard = (to: Route, from: Route) =>
    new Promise<GuardReturn>((resolve) => {
      calls.push({ to: to.path, from: from.path, resolve });
    });
  return { guard, calls };
}

/** Guard backed by a routing table; anything unmapped is allowed. */
function tableGuard(table: Record<string, GuardReturn>): Guard {
  return (to) => (to.path in table ? table[to.path] : undefined);
}

function paths(router: Router): string[] {
  return router.history().map((r) => r.path);
}

test("guards run strictly in registration order, one at a time", async () => {
  const router = new Router("/");
  const first = controllableGuard();
  const second = controllableGuard();
  router.beforeEach(first.guard);
  router.beforeEach(second.guard);

  const nav = router.navigate("/a");
  await Promise.resolve();
  assert.equal(first.calls.length, 1);
  assert.equal(second.calls.length, 0, "second guard must wait for the first");

  first.calls[0].resolve(undefined);
  await Promise.resolve();
  assert.equal(second.calls.length, 1);
  assert.deepEqual(
    second.calls.map((c) => [c.to, c.from]),
    [["/a", "/"]],
  );

  second.calls[0].resolve(true);
  const result = await nav;
  assert.deepEqual(result, { status: "committed", route: { path: "/a" } });
  assert.equal(router.current.path, "/a");
});

test("allow commits history exactly once", async () => {
  const router = new Router("/");
  router.beforeEach(() => undefined);
  router.beforeEach(async () => true);

  const result = await router.navigate("/a");
  assert.equal(result.status, "committed");
  assert.deepEqual(paths(router), ["/", "/a"]);
  assert.equal(router.index, 1);
});

test("reject cancels: address and history stay untouched", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  router.beforeEach(tableGuard({ "/blocked": false }));

  const result = await router.navigate("/blocked");
  assert.deepEqual(result, { status: "cancelled", reason: "rejected" });
  assert.equal(router.current.path, "/a");
  assert.deepEqual(paths(router), ["/", "/a"]);
});

test("redirect commits only the final target, exactly once", async () => {
  const router = new Router("/");
  router.beforeEach(tableGuard({ "/old": "/new" }));

  const result = await router.navigate("/old");
  assert.deepEqual(result, { status: "committed", route: { path: "/new" } });
  assert.equal(router.current.path, "/new");
  assert.deepEqual(paths(router), ["/", "/new"], "intermediate target never enters history");
});

test("nested redirects /a -> /b -> /c commit /c once", async () => {
  const router = new Router("/");
  router.beforeEach(tableGuard({ "/a": "/b", "/b": { redirect: "/c" } }));

  const result = await router.navigate("/a");
  assert.deepEqual(result, { status: "committed", route: { path: "/c" } });
  assert.deepEqual(paths(router), ["/", "/c"]);
});

test("redirect loop is detected per round and cancels without committing", async () => {
  const router = new Router("/");
  router.beforeEach(tableGuard({ "/a": "/b", "/b": "/a" }));

  const result = await router.navigate("/a");
  assert.deepEqual(result, { status: "cancelled", reason: "redirect-loop" });
  assert.equal(router.current.path, "/");
  assert.deepEqual(paths(router), ["/"]);
});

test("self-redirect is a loop too", async () => {
  const router = new Router("/");
  router.beforeEach(tableGuard({ "/a": "/a" }));
  const result = await router.navigate("/a");
  assert.deepEqual(result, { status: "cancelled", reason: "redirect-loop" });
});

test("visited set is per round: same target allowed again in a later navigation", async () => {
  const router = new Router("/");
  const table: Record<string, GuardReturn> = { "/a": "/b" };
  router.beforeEach(tableGuard(table));

  await router.navigate("/a"); // redirects to /b
  delete table["/a"];
  const result = await router.navigate("/a"); // now allowed directly
  assert.equal(result.status, "committed");
  assert.deepEqual(paths(router), ["/", "/b", "/a"]);
});

test("a new navigation supersedes: late allow from the old guard cannot move the address", async () => {
  const router = new Router("/");
  const slow = controllableGuard();
  const remove = router.beforeEach(slow.guard);

  const staleNav = router.navigate("/slow");
  await Promise.resolve();
  assert.equal(slow.calls.length, 1);

  remove(); // the fast navigation faces no guards
  const fastNav = router.navigate("/fast");
  assert.deepEqual(await fastNav, { status: "committed", route: { path: "/fast" } });

  // The old guard's promise arrives late and says "allow" — must be ignored.
  slow.calls[0].resolve(true);
  assert.deepEqual(await staleNav, { status: "cancelled", reason: "superseded" });
  assert.equal(router.current.path, "/fast");
  assert.deepEqual(paths(router), ["/", "/fast"], "no extra history entry from the stale round");
});

test("a late redirect from a superseded navigation is ignored as well", async () => {
  const router = new Router("/");
  const slow = controllableGuard();
  const remove = router.beforeEach(slow.guard);

  const staleNav = router.navigate("/slow");
  await Promise.resolve();
  remove();
  await router.navigate("/fast");

  slow.calls[0].resolve("/evil");
  assert.deepEqual(await staleNav, { status: "cancelled", reason: "superseded" });
  assert.equal(router.current.path, "/fast");
  assert.deepEqual(paths(router), ["/", "/fast"]);
});

test("back() traverses history without adding entries", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  await router.navigate("/b");

  const result = await router.back();
  assert.deepEqual(result, { status: "committed", route: { path: "/a" } });
  assert.equal(router.current.path, "/a");
  assert.deepEqual(paths(router), ["/", "/a", "/b"]);
  assert.equal(router.index, 1);

  const fwd = await router.forward();
  assert.equal(fwd.status, "committed");
  assert.equal(router.current.path, "/b");
});

test("a guard can cancel back()", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  router.beforeEach((to, from) => (from.path === "/a" && to.path === "/" ? false : undefined));

  const result = await router.back();
  assert.deepEqual(result, { status: "cancelled", reason: "rejected" });
  assert.equal(router.current.path, "/a");
  assert.equal(router.index, 1);
});

test("a guard can redirect back() into a fresh push", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  router.beforeEach((to, from) =>
    from.path === "/a" && to.path === "/" ? "/elsewhere" : undefined,
  );

  const result = await router.back();
  assert.deepEqual(result, { status: "committed", route: { path: "/elsewhere" } });
  assert.deepEqual(paths(router), ["/", "/a", "/elsewhere"]);
  assert.equal(router.current.path, "/elsewhere");
});

test("back() out of range is cancelled without touching state", async () => {
  const router = new Router("/");
  const result = await router.back();
  assert.deepEqual(result, { status: "cancelled", reason: "out-of-range" });
  assert.equal(router.current.path, "/");
  assert.deepEqual(paths(router), ["/"]);
});

test("push from the middle of history drops forward entries", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  await router.navigate("/b");
  await router.back();

  await router.navigate("/c");
  assert.deepEqual(paths(router), ["/", "/a", "/c"]);
  assert.equal(router.canGoForward, false);
});

test("replace swaps the current record instead of pushing", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  const result = await router.navigate("/b", { replace: true });
  assert.equal(result.status, "committed");
  assert.deepEqual(paths(router), ["/", "/b"]);
  assert.equal(router.index, 1);
});

test("superseded back() cannot move the cursor after a newer navigation commits", async () => {
  const router = new Router("/");
  await router.navigate("/a");
  await router.navigate("/b");

  const slow = controllableGuard();
  const remove = router.beforeEach(slow.guard);
  const staleBack = router.back();
  await Promise.resolve();
  assert.equal(slow.calls.length, 1);

  remove();
  await router.navigate("/c"); // cursor never moved, so /c pushes after /b

  slow.calls[0].resolve(true);
  assert.deepEqual(await staleBack, { status: "cancelled", reason: "superseded" });
  assert.equal(router.current.path, "/c");
  assert.deepEqual(paths(router), ["/", "/a", "/b", "/c"]);
});

test("unregistering a guard removes it from the chain", async () => {
  const router = new Router("/");
  const remove = router.beforeEach(() => false);
  remove();
  const result: NavigationResult = await router.navigate("/a");
  assert.equal(result.status, "committed");
});
