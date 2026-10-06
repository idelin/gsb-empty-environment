import { test } from "node:test";
import assert from "node:assert/strict";
import {
  Navigator,
  type Guard,
  type GuardResult,
  type NavOutcome,
} from "../src/navigation.ts";

/** A guard whose decisions are resolved manually by the test. */
interface PendingCall {
  to: string;
  from: string;
  resolve: (result: GuardResult) => void;
}

function controllableGuard() {
  const calls: PendingCall[] = [];
  const guard: Guard = (to, from) =>
    new Promise<GuardResult>((resolve) => {
      calls.push({ to, from, resolve });
    });
  return {
    guard,
    calls,
    /** Resolve the pending call for `to` (or the oldest one). */
    settle(result: GuardResult, to?: string): void {
      const at = to === undefined ? 0 : calls.findIndex((c) => c.to === to);
      assert.notEqual(at, -1, `expected a pending guard call for ${to}`);
      const [call] = calls.splice(at, 1);
      assert.ok(call, "expected a pending guard call");
      call.resolve(result);
    },
  };
}

/** Flush the microtask queue so started navigations reach their first guard. */
const tick = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

test("guards run sequentially in registration order", async () => {
  const nav = new Navigator("/");
  const order: string[] = [];
  nav.addGuard(() => {
    order.push("g1");
  });
  nav.addGuard(async () => {
    order.push("g2");
  });
  nav.addGuard(() => {
    order.push("g3");
    return true;
  });

  const outcome = await nav.navigate("/a");
  assert.deepEqual(outcome, { status: "committed", path: "/a" });
  assert.deepEqual(order, ["g1", "g2", "g3"]);
  assert.equal(nav.current, "/a");
});

test("guards receive (to, from); from stays at the committed address", async () => {
  const nav = new Navigator("/home");
  const seen: Array<[string, string]> = [];
  nav.addGuard((to, from) => {
    seen.push([to, from]);
  });

  await nav.navigate("/next");
  assert.deepEqual(seen, [["/next", "/home"]]);
});

test("a denying guard cancels: address and history stay untouched", async () => {
  const nav = new Navigator("/");
  nav.addGuard(() => true);
  nav.addGuard(() => false);
  const reached: string[] = [];
  nav.addGuard(() => {
    reached.push("g3");
  });

  const outcome = await nav.navigate("/secret");
  assert.deepEqual(outcome, {
    status: "cancelled",
    target: "/secret",
    reason: undefined,
  });
  assert.equal(nav.current, "/");
  assert.deepEqual(nav.history, ["/"]);
  assert.deepEqual(reached, []); // guards after the denying one never run
});

test("redirect restarts the guard chain for the new target", async () => {
  const nav = new Navigator("/");
  const seen: string[] = [];
  nav.addGuard((to) => {
    seen.push(to);
    if (to === "/old") return "/new";
  });

  const outcome = await nav.navigate("/old");
  assert.deepEqual(outcome, { status: "committed", path: "/new" });
  assert.deepEqual(seen, ["/old", "/new"]);
  assert.equal(nav.current, "/new");
  assert.deepEqual(nav.history, ["/", "/new"]);
});

test("nested redirects commit history exactly once", async () => {
  const nav = new Navigator("/");
  nav.addGuard((to) => {
    if (to === "/a") return { type: "redirect", to: "/b" };
    if (to === "/b") return "/c";
  });

  const outcome = await nav.navigate("/a");
  assert.deepEqual(outcome, { status: "committed", path: "/c" });
  assert.equal(nav.current, "/c");
  // Only one entry appended despite two redirects.
  assert.deepEqual(nav.history, ["/", "/c"]);
});

test("redirect loops are detected within the current round", async () => {
  const nav = new Navigator("/");
  nav.addGuard((to) => {
    if (to === "/a") return "/b";
    if (to === "/b") return "/a";
  });

  const outcome = await nav.navigate("/a");
  assert.deepEqual(outcome, { status: "loop", chain: ["/a", "/b", "/a"] });
  assert.equal(nav.current, "/");
  assert.deepEqual(nav.history, ["/"]);
});

test("loop detection is per-round: revisiting targets in a new round is fine", async () => {
  const nav = new Navigator("/");
  nav.addGuard((to) => (to === "/x" ? "/y" : undefined));

  const first = await nav.navigate("/x");
  assert.deepEqual(first, { status: "committed", path: "/y" });

  // A fresh round may visit the same targets again without a loop error.
  const second = await nav.navigate("/x");
  assert.deepEqual(second, { status: "committed", path: "/y" });
  assert.deepEqual(nav.history, ["/", "/y", "/y"]);
});

test("a new navigation supersedes a pending one; late promise cannot move the address", async () => {
  const nav = new Navigator("/");
  const gate = controllableGuard();
  nav.addGuard(gate.guard);

  const slowPromise = nav.navigate("/slow");
  await tick();
  assert.equal(gate.calls.length, 1);
  assert.equal(gate.calls[0].to, "/slow");

  // A newer navigation starts while the first guard is still pending.
  const fastPromise = nav.navigate("/fast");
  await tick();
  assert.equal(gate.calls.length, 2);
  gate.settle(true, "/fast"); // resolves the /fast navigation
  assert.deepEqual(await fastPromise, { status: "committed", path: "/fast" });
  assert.equal(nav.current, "/fast");

  // The stale guard resolves late with "allow" — it must not commit.
  gate.settle(true, "/slow");
  assert.deepEqual(await slowPromise, { status: "superseded" });
  assert.equal(nav.current, "/fast");
  assert.deepEqual(nav.history, ["/", "/fast"]);
});

test("a stale round's late redirect is ignored as well", async () => {
  const nav = new Navigator("/");
  const gate = controllableGuard();
  nav.addGuard(gate.guard);

  const stalePromise = nav.navigate("/old");
  await tick();
  const freshPromise = nav.navigate("/fresh");
  await tick();
  gate.settle(true, "/fresh"); // fresh round commits
  await freshPromise;

  gate.settle("/elsewhere", "/old"); // stale round tries to redirect late
  assert.deepEqual(await stalePromise, { status: "superseded" });
  assert.equal(nav.current, "/fresh");
  assert.deepEqual(nav.history, ["/", "/fresh"]);
});

test("back() commits a move to the previous entry", async () => {
  const nav = new Navigator("/");
  await nav.navigate("/a");
  await nav.navigate("/b");
  assert.equal(nav.current, "/b");

  const outcome = await nav.back();
  assert.deepEqual(outcome, { status: "committed", path: "/a" });
  assert.equal(nav.current, "/a");
  assert.deepEqual(nav.history, ["/", "/a", "/b"]); // entries kept, index moved
});

test("back() runs guards and a denial keeps the current address", async () => {
  const nav = new Navigator("/");
  await nav.navigate("/a");
  await nav.navigate("/b");

  const remove = nav.addGuard((to) =>
    to === "/a" ? { type: "deny", reason: "locked" } : undefined,
  );
  const outcome = await nav.back();
  assert.deepEqual(outcome, {
    status: "cancelled",
    target: "/a",
    reason: "locked",
  });
  assert.equal(nav.current, "/b");

  remove();
  const retry = await nav.back();
  assert.deepEqual(retry, { status: "committed", path: "/a" });
  assert.equal(nav.current, "/a");
});

test("back() at the start of history is a no-op cancellation", async () => {
  const nav = new Navigator("/");
  const outcome = await nav.back();
  assert.equal(outcome.status, "cancelled");
  assert.equal(nav.current, "/");
});

test("back() can be superseded by a newer navigation", async () => {
  const nav = new Navigator("/");
  await nav.navigate("/a");

  const gate = controllableGuard();
  nav.addGuard(gate.guard);

  const backPromise = nav.back();
  await tick();
  assert.equal(gate.calls[0].to, "/");

  const forwardPromise = nav.navigate("/else");
  await tick();
  gate.settle(true, "/else"); // forward navigation commits
  assert.deepEqual(await forwardPromise, {
    status: "committed",
    path: "/else",
  });

  gate.settle(true, "/"); // stale back() resolves late
  assert.deepEqual(await backPromise, { status: "superseded" });
  assert.equal(nav.current, "/else");
});

test("pushing after back() truncates forward entries", async () => {
  const nav = new Navigator("/");
  await nav.navigate("/a");
  await nav.navigate("/b");
  await nav.back();
  await nav.navigate("/c");
  assert.deepEqual(nav.history, ["/", "/a", "/c"]);
  assert.equal(nav.current, "/c");
});

test("redirect during back() commits the redirected target as a back-move", async () => {
  const nav = new Navigator("/");
  await nav.navigate("/a");
  await nav.navigate("/b");

  nav.addGuard((to) => (to === "/a" ? "/" : undefined));
  const outcome: NavOutcome = await nav.back();
  // The guard redirected the back target from /a to /; since the final
  // target is not the previous history entry, it is pushed instead.
  assert.deepEqual(outcome, { status: "committed", path: "/" });
  assert.equal(nav.current, "/");
  assert.deepEqual(nav.history, ["/", "/a", "/b", "/"]);
});
