import assert from "node:assert/strict";
import test from "node:test";
import {
  BRIDGE_READY_TYPE,
  BRIDGE_REQUEST_TYPE,
  BRIDGE_RESPONSE_TYPE,
  OneTimePageBridge,
  type PrivilegedDispatcher,
  type ValidatedPageRequest,
} from "../src/bridge.ts";
import { loadManifestConfig } from "../src/config.ts";
import {
  delay,
  extensionManifest,
  FakeWindow,
  hello,
  waitForPortMessage,
} from "./helpers.ts";

const ORIGIN = "https://site.example";
const ALLOWED = "https://api.example.com";

function setup(dispatch: PrivilegedDispatcher) {
  const window = new FakeWindow(ORIGIN);
  const config = loadManifestConfig(extensionManifest([ALLOWED]));
  const bridge = new OneTimePageBridge({
    hostWindow: window,
    expectedOrigin: ORIGIN,
    config,
    dispatch,
  });
  bridge.start();
  return { bridge, config, window };
}

function openPort(window: FakeWindow, requestId = "hello-1") {
  window.emitMessage({ data: hello(requestId) });
  const posted = window.posted.at(-1);
  assert.ok(posted);
  assert.equal(posted.targetOrigin, ORIGIN);
  assert.equal(posted.ports.length, 1);
  const ready = posted.message as Record<string, unknown>;
  assert.equal(ready.type, BRIDGE_READY_TYPE);
  return {
    ready,
    sessionId: ready.sessionId as string,
    pagePort: posted.ports[0],
  };
}

test("legitimate page interaction gets one scoped port and an allowlisted result", async () => {
  const requests: ValidatedPageRequest[] = [];
  const { bridge, window } = setup(async (request) => {
    requests.push(request);
    return { ok: true, value: "relay-ok" };
  });

  try {
    const { sessionId, pagePort } = openPort(window);
    const responsePromise = waitForPortMessage(pagePort);
    pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId,
      requestId: "request-1",
      operation: "relay",
      targetOrigin: ALLOWED,
      body: "hello",
    });
    const response = (await responsePromise) as Record<string, unknown>;

    assert.equal(response.type, BRIDGE_RESPONSE_TYPE);
    assert.equal(response.sessionId, sessionId);
    assert.equal(response.requestId, "request-1");
    assert.equal(response.ok, true);
    assert.equal(response.value, "relay-ok");
    assert.equal(requests.length, 1);
    assert.equal(requests[0].targetOrigin, ALLOWED);
    assert.equal(Object.isFrozen(requests[0]), true);
    assert.equal(window.posted[0].targetOrigin, ORIGIN);
    assert.notEqual(window.posted[0].targetOrigin, "*");
  } finally {
    bridge.stop();
  }
});

test("a page-controlled destination outside the manifest is denied", async () => {
  let calls = 0;
  const { bridge, window } = setup(() => {
    calls += 1;
    return { ok: true, value: "should-not-run" };
  });

  try {
    const { sessionId, pagePort } = openPort(window);
    pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId,
      requestId: "attack-1",
      operation: "relay",
      targetOrigin: "https://attacker.example",
      body: "steal",
    });
    await delay(20);
    assert.equal(calls, 0);
  } finally {
    bridge.stop();
  }
});

test("forged source, origin and transferred ports are ignored", async () => {
  let calls = 0;
  const { bridge, window } = setup(() => {
    calls += 1;
    return { ok: true, value: "no" };
  });
  const iframe = new FakeWindow(ORIGIN);
  const forged = new MessageChannel();

  try {
    window.emitMessage({
      data: hello("cross-frame"),
      source: iframe,
    });
    window.emitMessage({
      data: hello("wrong-origin"),
      origin: "https://attacker.example",
    });
    window.emitMessage({
      data: hello("forged-port"),
      ports: [forged.port1],
    });

    await delay(20);
    assert.equal(window.posted.length, 0);
    assert.equal(bridge.hasActiveSession, false);
    assert.equal(calls, 0);
  } finally {
    forged.port1.close();
    forged.port2.close();
    bridge.stop();
  }
});

test("each handshake revokes the previous one-shot port", async () => {
  let calls = 0;
  const { bridge, window } = setup(() => {
    calls += 1;
    return { ok: true, value: "ok" };
  });

  try {
    const first = openPort(window, "hello-1");
    const second = openPort(window, "hello-2");
    assert.notEqual(first.sessionId, second.sessionId);
    assert.equal(window.posted.length, 2);

    first.pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId: first.sessionId,
      requestId: "stale",
      operation: "relay",
      targetOrigin: ALLOWED,
      body: "stale",
    });
    await delay(20);
    assert.equal(calls, 0);

    const responsePromise = waitForPortMessage(second.pagePort);
    second.pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId: second.sessionId,
      requestId: "fresh",
      operation: "relay",
      targetOrigin: ALLOWED,
      body: "fresh",
    });
    const response = (await responsePromise) as Record<string, unknown>;
    assert.equal(response.ok, true);
    assert.equal(calls, 1);
  } finally {
    bridge.stop();
  }
});

test("a one-time port accepts only its first request", async () => {
  let calls = 0;
  let release: (() => void) | undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const { bridge, window } = setup(async () => {
    calls += 1;
    await gate;
    return { ok: true, value: "once" };
  });

  try {
    const { sessionId, pagePort } = openPort(window, "once-hello");
    const responsePromise = waitForPortMessage(pagePort);
    for (const requestId of ["first", "second"]) {
      pagePort.postMessage({
        type: BRIDGE_REQUEST_TYPE,
        protocolVersion: 1,
        sessionId,
        requestId,
        operation: "relay",
        targetOrigin: ALLOWED,
        body: requestId,
      });
    }
    release?.();
    const response = (await responsePromise) as Record<string, unknown>;
    await delay(20);

    assert.equal(response.requestId, "first");
    assert.equal(calls, 1);
  } finally {
    bridge.stop();
  }
});

test("prototype-polluted, oversized and extra-field requests fail closed", async () => {
  let calls = 0;
  const { bridge, window } = setup(() => {
    calls += 1;
    return { ok: true, value: "no" };
  });

  try {
    {
      const { sessionId, pagePort } = openPort(window, "proto-hello");
      const polluted = JSON.parse(
        `{"type":"${BRIDGE_REQUEST_TYPE}","protocolVersion":1,"sessionId":"${sessionId}","requestId":"proto","operation":"relay","targetOrigin":"${ALLOWED}","body":"x","__proto__":{"admin":true}}`,
      );
      pagePort.postMessage(polluted);
      await delay(20);
    }

    {
      const { sessionId, pagePort } = openPort(window, "long-hello");
      pagePort.postMessage({
        type: BRIDGE_REQUEST_TYPE,
        protocolVersion: 1,
        sessionId,
        requestId: "long",
        operation: "relay",
        targetOrigin: ALLOWED,
        body: "x".repeat(1_025),
      });
      await delay(20);
    }

    {
      const { sessionId, pagePort } = openPort(window, "extra-hello");
      pagePort.postMessage({
        type: BRIDGE_REQUEST_TYPE,
        protocolVersion: 1,
        sessionId,
        requestId: "extra",
        operation: "relay",
        targetOrigin: ALLOWED,
        body: "x",
        headers: { Authorization: "secret" },
      });
      await delay(20);
    }

    assert.equal(calls, 0);
  } finally {
    bridge.stop();
  }
});

test("wrong page field types are rejected before dispatch", async () => {
  let calls = 0;
  const { bridge, window } = setup(() => {
    calls += 1;
    return { ok: true, value: "no" };
  });

  try {
    {
      const { sessionId, pagePort } = openPort(window, "number-origin");
      pagePort.postMessage({
        type: BRIDGE_REQUEST_TYPE,
        protocolVersion: 1,
        sessionId,
        requestId: "number-origin",
        operation: "relay",
        targetOrigin: 42,
        body: "x",
      });
      await delay(20);
    }

    {
      const { sessionId, pagePort } = openPort(window, "object-body");
      pagePort.postMessage({
        type: BRIDGE_REQUEST_TYPE,
        protocolVersion: 1,
        sessionId,
        requestId: "object-body",
        operation: "relay",
        targetOrigin: ALLOWED,
        body: { toString: "not a string" },
      });
      await delay(20);
    }

    assert.equal(calls, 0);
  } finally {
    bridge.stop();
  }
});

test("bridge rejects an origin that differs from the host window", () => {
  const window = new FakeWindow(ORIGIN);
  const config = loadManifestConfig(extensionManifest([ALLOWED]));
  assert.throws(
    () =>
      new OneTimePageBridge({
        hostWindow: window,
        expectedOrigin: "https://other.example",
        config,
        dispatch: () => ({ ok: true, value: "no" }),
      }),
  );
});
