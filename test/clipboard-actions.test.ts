import { test } from "node:test";
import assert from "node:assert/strict";

import {
  ClipboardActions,
  PausableClipboardPermissionGranter,
  createClipboardActions,
  type ClipboardPermissionDecision,
  type ClipboardPermissionGranter,
  type ClipboardPermissionRequest,
} from "../index.ts";
import {
  RecordingClipboard,
  SharedFocus,
  TabEnvironment,
  TestClock,
} from "./helpers.ts";

function setup() {
  const clock = new TestClock(1_000);
  const focus = new SharedFocus("tab-1");
  const tab1 = new TabEnvironment("tab-1", focus, clock);
  const tab2 = new TabEnvironment("tab-2", focus, clock);
  const granter = new PausableClipboardPermissionGranter();
  const clipboard = new RecordingClipboard();

  const createActions = (environment: TabEnvironment): ClipboardActions =>
    createClipboardActions({
      environment,
      permissions: granter,
      systemClipboard: clipboard,
    });

  return {
    clock,
    focus,
    tab1,
    tab2,
    granter,
    clipboard,
    actions1: createActions(tab1),
    actions2: createActions(tab2),
  };
}

test("copy waits for the callback and verifies every binding", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("hello");
  assert.equal(s.clipboard.writes.length, 0);
  assert.equal(s.granter.pendingCount, 1);

  const request = s.granter.pendingRequests[0];
  assert.equal(request.operation, "write");
  assert.equal(request.tabId, "tab-1");
  assert.equal(request.documentId, "tab-1-document-1");
  assert.equal(request.documentGeneration, 1);
  assert.equal(request.focusGeneration, s.focus.generation);
  assert.equal(request.activationId, "tab-1-activation-1");
  assert.equal(request.activationExpiresAt, 6_000);
  assert.equal(request.requestedAt, 1_000);
  assert.equal(request.token.length > 0, true);

  assert.equal(s.granter.grant(request.requestId), true);
  const result = await copy;

  assert.equal(result.ok, true);
  assert.deepEqual(s.clipboard.writes, ["hello"]);
  assert.equal(s.actions1.pendingCount, 0);
});

test("a stale request cannot consume another tab's permission", async () => {
  const s = setup();
  s.tab1.activate();
  s.tab2.activate();

  s.focus.moveTo("tab-1");
  const copyTab1 = s.actions1.copy("tab-1 text");

  s.focus.moveTo("tab-2");
  const copyTab2 = s.actions2.copy("tab-2 text");

  const [requestTab1, requestTab2] = s.granter.pendingRequests;
  assert.equal(s.granter.pendingCount, 2);
  assert.equal(requestTab1.tabId, "tab-1");
  assert.equal(requestTab2.tabId, "tab-2");

  // The first tab's callback arrives while the second tab is focused. It fails
  // and must leave the second tab's still-pending grant untouched.
  assert.equal(s.granter.grant(requestTab1.requestId), true);
  const staleResult = await copyTab1;
  assert.equal(staleResult.ok, false);
  assert.equal(
    staleResult.ok ? null : staleResult.reason,
    "focus-change",
  );
  assert.equal(s.clipboard.writes.length, 0);

  assert.equal(s.granter.grant(requestTab2.requestId), true);
  const freshResult = await copyTab2;
  assert.equal(freshResult.ok, true);
  assert.deepEqual(s.clipboard.writes, ["tab-2 text"]);
});

test("a stale failure cannot discard an already-granted sibling", async () => {
  const s = setup();
  s.tab1.activate();
  s.tab2.activate();

  s.focus.moveTo("tab-1");
  const copyTab1 = s.actions1.copy("stale");
  s.focus.moveTo("tab-2");
  const copyTab2 = s.actions2.copy("granted");
  const [requestTab1, requestTab2] = s.granter.pendingRequests;

  // Grant the second tab first, then invalidate the first tab by navigating
  // it. The first tab's failure must not remove the second tab's grant.
  assert.equal(s.granter.grant(requestTab2.requestId), true);
  s.tab1.navigate("tab-1-document-2");
  assert.equal(s.granter.grant(requestTab1.requestId), true);

  const [staleResult, grantedResult] = await Promise.all([
    copyTab1,
    copyTab2,
  ]);
  assert.equal(staleResult.ok, false);
  assert.equal(staleResult.ok ? null : staleResult.reason, "navigation");
  assert.equal(grantedResult.ok, true);
  assert.deepEqual(s.clipboard.writes, ["granted"]);
});

test("navigation after the prompt invalidates the request", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("before navigation");
  const request = s.granter.pendingRequests[0];

  s.tab1.navigate("tab-1-document-2");
  assert.equal(s.granter.grant(request.requestId), true);

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "navigation");
  assert.equal(s.clipboard.writes.length, 0);
});

test("focus moving away and back still invalidates the request", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("focused text");
  const request = s.granter.pendingRequests[0];

  s.focus.moveTo("tab-2");
  s.focus.moveTo("tab-1");
  assert.equal(s.granter.grant(request.requestId), true);

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "focus-change");
  assert.equal(s.clipboard.writes.length, 0);
});

test("an expired user activation cannot authorize a write", async () => {
  const s = setup();
  s.tab1.activate(5_000);

  const copy = s.actions1.copy("too late");
  const request = s.granter.pendingRequests[0];

  s.clock.advance(5_000);
  assert.equal(s.granter.grant(request.requestId), true);

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "activation-expired");
  assert.equal(s.clipboard.writes.length, 0);
});

test("a replacement activation cannot authorize an older request", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("old activation");
  const request = s.granter.pendingRequests[0];

  s.tab1.activate();
  assert.equal(s.granter.grant(request.requestId), true);

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "activation-changed");
  assert.equal(s.clipboard.writes.length, 0);
});

test("cancelling a copy action releases only that request", async () => {
  const s = setup();
  s.tab1.activate();
  const controller = new AbortController();

  const cancelled = s.actions1.copy("cancelled text", {
    signal: controller.signal,
  });
  const cancelledRequest = s.granter.pendingRequests[0];

  controller.abort();
  const cancelledResult = await cancelled;
  assert.equal(cancelledResult.ok, false);
  assert.equal(
    cancelledResult.ok ? null : cancelledResult.reason,
    "cancelled",
  );
  assert.equal(s.clipboard.writes.length, 0);
  assert.equal(s.granter.pendingCount, 0);

  // A late host callback for the cancelled request is inert.
  assert.equal(s.granter.grant(cancelledRequest.requestId), false);

  // A separate request in the same document can still be granted.
  const allowed = s.actions1.copy("allowed text");
  const allowedRequest = s.granter.pendingRequests[0];
  assert.equal(s.granter.grant(allowedRequest.requestId), true);
  assert.equal((await allowed).ok, true);
  assert.deepEqual(s.clipboard.writes, ["allowed text"]);
});

class StubbornGranter implements ClipboardPermissionGranter {
  readonly requests: ClipboardPermissionRequest[] = [];
  private readonly resolvers = new Map<
    string,
    (decision: ClipboardPermissionDecision) => void
  >();

  request(
    request: ClipboardPermissionRequest,
  ): Promise<ClipboardPermissionDecision> {
    this.requests.push(request);
    return new Promise<ClipboardPermissionDecision>((resolve) => {
      this.resolvers.set(request.requestId, resolve);
    });
  }

  // Deliberately ignores cancellation, simulating a host whose prompt remains
  // open after the page-side action has been aborted.
  cancel(): void {}

  grant(requestId: string): void {
    const request = this.requests.find(
      (candidate) => candidate.requestId === requestId,
    );
    if (request === undefined) {
      throw new Error("unknown request");
    }
    this.resolvers.get(requestId)?.({
      status: "granted",
      requestId,
      token: request.token,
    });
  }
}

test("a callback arriving after cancellation never writes", async () => {
  const clock = new TestClock();
  const focus = new SharedFocus("tab-1");
  const tab1 = new TabEnvironment("tab-1", focus, clock);
  tab1.activate();
  const clipboard = new RecordingClipboard();
  const granter = new StubbornGranter();
  const actions = createClipboardActions({
    environment: tab1,
    permissions: granter,
    systemClipboard: clipboard,
  });
  const controller = new AbortController();

  const copy = actions.copy("late text", { signal: controller.signal });
  const request = granter.requests[0];
  controller.abort();

  assert.equal((await copy).ok, false);
  granter.grant(request.requestId);
  await Promise.resolve();
  await Promise.resolve();

  assert.equal(clipboard.writes.length, 0);
});

test("a grant carrying another request's token is rejected", async () => {
  const s = setup();
  s.tab1.activate();

  const first = s.actions1.copy("first");
  const second = s.actions1.copy("second");
  const [firstRequest, secondRequest] = s.granter.pendingRequests;

  s.granter.settle(firstRequest.requestId, {
    status: "granted",
    requestId: secondRequest.requestId,
    token: secondRequest.token,
  });

  const firstResult = await first;
  assert.equal(firstResult.ok, false);
  assert.equal(
    firstResult.ok ? null : firstResult.reason,
    "permission-mismatch",
  );
  assert.equal(s.clipboard.writes.length, 0);

  assert.equal(s.granter.grant(secondRequest.requestId), true);
  assert.equal((await second).ok, true);
  assert.deepEqual(s.clipboard.writes, ["second"]);
});

test("a forged token from the same request id is rejected", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("forged");
  const request = s.granter.pendingRequests[0];
  s.granter.settle(request.requestId, {
    status: "granted",
    requestId: request.requestId,
    token: "forged-token",
  });

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "permission-mismatch");
  assert.equal(s.clipboard.writes.length, 0);
});

test("denied and cancelled decisions do not touch the clipboard", async () => {
  const s = setup();
  s.tab1.activate();

  const denied = s.actions1.copy("denied");
  const deniedRequest = s.granter.pendingRequests[0];
  assert.equal(s.granter.deny(deniedRequest.requestId), true);
  const deniedResult = await denied;
  assert.equal(deniedResult.ok, false);
  assert.equal(
    deniedResult.ok ? null : deniedResult.reason,
    "permission-denied",
  );

  const cancelled = s.actions1.copy("cancelled");
  const cancelledRequest = s.granter.pendingRequests[0];
  assert.equal(s.granter.cancel(cancelledRequest.requestId), true);
  const cancelledResult = await cancelled;
  assert.equal(cancelledResult.ok, false);
  assert.equal(
    cancelledResult.ok ? null : cancelledResult.reason,
    "cancelled",
  );

  assert.equal(s.clipboard.writes.length, 0);
});

test("a duplicate callback invokes the system clipboard only once", async () => {
  const s = setup();
  s.tab1.activate();

  const copy = s.actions1.copy("once");
  const request = s.granter.pendingRequests[0];

  assert.equal(s.granter.grant(request.requestId), true);
  assert.equal(s.granter.grant(request.requestId), false);

  assert.equal((await copy).ok, true);
  assert.deepEqual(s.clipboard.writes, ["once"]);
});

test("read actions use the same document and focus checks", async () => {
  const s = setup();
  s.tab1.activate();

  const staleRead = s.actions1.read();
  const staleRequest = s.granter.pendingRequests[0];
  s.tab1.navigate();
  assert.equal(s.granter.grant(staleRequest.requestId), true);

  const staleResult = await staleRead;
  assert.equal(staleResult.ok, false);
  assert.equal(staleResult.ok ? null : staleResult.reason, "navigation");
  assert.equal(s.clipboard.reads, 0);

  const freshRead = s.actions1.read();
  const freshRequest = s.granter.pendingRequests[0];
  assert.equal(s.granter.grant(freshRequest.requestId), true);
  const freshResult = await freshRead;
  assert.equal(freshResult.ok, true);
  assert.equal(freshResult.ok ? freshResult.value : null, "clipboard text");
  assert.equal(s.clipboard.reads, 1);
});

test("no prompt is opened without activation or focus", async () => {
  const s = setup();

  const inactive = await s.actions1.copy("inactive");
  assert.equal(inactive.ok, false);
  assert.equal(inactive.ok ? null : inactive.reason, "inactive");
  assert.equal(s.granter.pendingCount, 0);

  s.tab1.activate();
  s.focus.moveTo("tab-2");
  const unfocused = await s.actions1.copy("unfocused");
  assert.equal(unfocused.ok, false);
  assert.equal(unfocused.ok ? null : unfocused.reason, "not-focused");
  assert.equal(s.granter.pendingCount, 0);
  assert.equal(s.clipboard.writes.length, 0);
});

test("system clipboard failures are contained after authorization", async () => {
  const s = setup();
  s.tab1.activate();
  s.clipboard.throwOnWrite = new Error("host denied write");

  const copy = s.actions1.copy("will fail");
  const request = s.granter.pendingRequests[0];
  assert.equal(s.granter.grant(request.requestId), true);

  const result = await copy;
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "clipboard-error");
  assert.equal(s.clipboard.writes.length, 0);
});

test("a failing permission prompt never reaches the clipboard", async () => {
  const clock = new TestClock();
  const focus = new SharedFocus("tab-1");
  const tab1 = new TabEnvironment("tab-1", focus, clock);
  tab1.activate();
  const clipboard = new RecordingClipboard();
  const granter: ClipboardPermissionGranter = {
    request(): Promise<ClipboardPermissionDecision> {
      return Promise.reject(new Error("prompt host failed"));
    },
  };
  const actions = createClipboardActions({
    environment: tab1,
    permissions: granter,
    systemClipboard: clipboard,
  });

  const result = await actions.copy("never copied");
  assert.equal(result.ok, false);
  assert.equal(result.ok ? null : result.reason, "permission-error");
  assert.equal(clipboard.writes.length, 0);
});
