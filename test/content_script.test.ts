import assert from "node:assert/strict";
import test from "node:test";
import {
  BRIDGE_READY_TYPE,
  BRIDGE_REQUEST_TYPE,
} from "../src/bridge.ts";
import {
  CONFIG_MESSAGE_TYPE,
  type SecurityConfig,
} from "../src/config.ts";
import { ExtensionContentScript } from "../src/content_script.ts";
import {
  delay,
  extensionManifest,
  FakeElement,
  FakeRuntime,
  FakeWindow,
  hello,
  waitForPortMessage,
} from "./helpers.ts";
import { buildDocument } from "./helpers.ts";

const EXTENSION_ID = "extension-id";
const ORIGIN = "https://site.example";
const ALLOWED_ONE = "https://api.example.com";
const ALLOWED_TWO = "https://cdn.example.com";

function setup() {
  const window = new FakeWindow(ORIGIN);
  const { document, body } = buildDocument([new FakeElement("main")]);
  const runtime = new FakeRuntime(
    EXTENSION_ID,
    extensionManifest([ALLOWED_ONE, ALLOWED_TWO]),
  );
  const requests: unknown[] = [];
  const script = new ExtensionContentScript({
    runtime,
    hostWindow: window,
    documentLike: document,
    dispatch: (request) => {
      requests.push(request);
      return { ok: true, value: "bridge-ok" };
    },
  });
  script.start();
  return { body, document, requests, runtime, script, window };
}

function trustedSender() {
  return {
    id: EXTENSION_ID,
    url: `chrome-extension://${EXTENSION_ID}/background.js`,
    origin: `chrome-extension://${EXTENSION_ID}`,
  };
}

function configMessage(
  config: Readonly<Record<string, unknown>>,
) {
  return {
    type: CONFIG_MESSAGE_TYPE,
    protocolVersion: 1,
    config,
  };
}

function openPort(window: FakeWindow) {
  window.emitMessage({ data: hello() });
  const posted = window.posted.at(-1);
  assert.ok(posted);
  const ready = posted.message as Record<string, unknown>;
  assert.equal(ready.type, BRIDGE_READY_TYPE);
  return {
    pagePort: posted.ports[0],
    sessionId: ready.sessionId as string,
  };
}

test("legitimate page interaction uses validated page data and trusted policy", async () => {
  const { requests, script, window } = setup();
  try {
    const { pagePort, sessionId } = openPort(window);
    const responsePromise = waitForPortMessage(pagePort);
    pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId,
      requestId: "status-1",
      operation: "status",
      targetOrigin: ALLOWED_ONE,
      body: "",
    });
    const response = (await responsePromise) as Record<string, unknown>;
    assert.equal(response.ok, true);
    assert.equal(response.value, "bridge-ok");
    assert.equal(requests.length, 1);
    assert.equal(script.isOperationEnabled("status"), true);
  } finally {
    script.stop();
  }
});

test("DOM overrides cannot change policy or redirect a bridge destination", async () => {
  const { body, document, requests, script, window } = setup();
  try {
    const head = document.children[0].children[0];
    head.append(
      new FakeElement("meta", {
        id: "extension-config",
        name: "extension_security",
      }),
    );
    const attackerMain = new FakeElement("main", {
      id: "extension-config",
      className: "trusted",
      name: "allowedOrigins",
    });
    body.append(attackerMain);

    const anchor = script.locatePageAnchor();
    assert.equal(anchor.ok, false);
    if (!anchor.ok) {
      assert.equal(anchor.reason, "ambiguous");
    }
    assert.deepEqual(script.config.allowedOrigins, [
      ALLOWED_ONE,
      ALLOWED_TWO,
    ]);

    const { pagePort, sessionId } = openPort(window);
    pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId,
      requestId: "dom-override",
      operation: "relay",
      targetOrigin: "https://attacker.example",
      body: "attacker",
    });
    await delay(20);
    assert.equal(requests.length, 0);
  } finally {
    script.stop();
  }
});

test("trusted messages narrow policy, while page senders and widened updates do not", () => {
  const { runtime, script } = setup();
  try {
    assert.deepEqual(
      script.handleTrustedRuntimeMessage(
        configMessage({ allowedOrigins: [ALLOWED_ONE] }),
        trustedSender(),
      ),
      { accepted: true },
    );
    assert.deepEqual(script.config.allowedOrigins, [ALLOWED_ONE]);

    const pageResult = script.handleTrustedRuntimeMessage(
      configMessage({ allowedOrigins: [] }),
      {
        id: EXTENSION_ID,
        url: "https://attacker.example/page",
        origin: "https://attacker.example",
      },
    );
    assert.equal(pageResult.accepted, false);
    assert.deepEqual(script.config.allowedOrigins, [ALLOWED_ONE]);

    const widenResult = script.handleTrustedRuntimeMessage(
      configMessage({ allowedOrigins: [ALLOWED_TWO] }),
      trustedSender(),
    );
    assert.equal(widenResult.accepted, false);

    runtime.emit(
      configMessage({ allowedOrigins: [] }),
      { id: EXTENSION_ID, url: "https://attacker.example/page" },
    );
    assert.deepEqual(script.config.allowedOrigins, [ALLOWED_ONE]);
  } finally {
    script.stop();
  }
});

test("narrowing an active policy revokes the current one-time port", async () => {
  const { requests, script, window } = setup();
  try {
    const { pagePort, sessionId } = openPort(window);
    const result = script.handleTrustedRuntimeMessage(
      configMessage({ allowedOperations: ["status"] }),
      trustedSender(),
    );
    assert.equal(result.accepted, true);

    pagePort.postMessage({
      type: BRIDGE_REQUEST_TYPE,
      protocolVersion: 1,
      sessionId,
      requestId: "revoked",
      operation: "relay",
      targetOrigin: ALLOWED_ONE,
      body: "x",
    });
    await delay(20);
    assert.equal(requests.length, 0);
    assert.deepEqual(script.config.allowedOperations, ["status"]);
  } finally {
    script.stop();
  }
});

test("cross-frame messages and exposed callbacks do not reach the privileged handler", async () => {
  const { requests, script, window } = setup();
  const iframe = new FakeWindow(ORIGIN);
  try {
    window.emitMessage({
      data: hello("cross-frame"),
      source: iframe,
    });
    await delay(20);

    assert.equal(window.posted.length, 0);
    assert.equal(requests.length, 0);
    assert.equal(
      Object.prototype.hasOwnProperty.call(window, "dispatch"),
      false,
    );
    assert.equal(
      Object.prototype.hasOwnProperty.call(window, "ExtensionContentScript"),
      false,
    );
  } finally {
    script.stop();
  }
});

test("invalid manifest fails closed before a listener is installed", () => {
  const window = new FakeWindow(ORIGIN);
  const { document } = buildDocument();
  const runtime = new FakeRuntime(EXTENSION_ID, {
    manifest_version: 3,
    host_permissions: ["https://api.example.com/*"],
    extension_security: {
      version: 1,
      allowedOrigins: ["https://evil.example"],
    },
  });

  assert.throws(() =>
    new ExtensionContentScript({
      runtime,
      hostWindow: window,
      documentLike: document,
      dispatch: () => ({ ok: true, value: "no" }),
    }),
  );
  assert.equal(runtime.listeners.size, 0);
});

test("config snapshots stay frozen after page and runtime activity", () => {
  const { runtime, script } = setup();
  try {
    const before: SecurityConfig = script.config;
    assert.equal(Object.isFrozen(before), true);
    runtime.emit(
      configMessage({ allowedOrigins: [] }),
      trustedSender(),
    );
    assert.equal(script.config.allowedOrigins.length, 0);
    assert.equal(Object.isFrozen(script.config), true);
  } finally {
    script.stop();
  }
});
