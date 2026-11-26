import test from "node:test";
import assert from "node:assert/strict";
import {
  CapabilityAuthority,
  RuntimeMessageGate,
  analyzeManifestSecurity,
  isRouteSafe,
  reviewRuntimeSecurity,
  verifyMessageRouteSecurity,
} from "../src/index.ts";
import type { ManifestV3, MessageRoute } from "../src/index.ts";

const APP = "https://app.example.com";
const BANK = "https://bank.example";
const ATTACKER = "https://attacker.example";
const SECRET = "0123456789abcdef0123456789abcdef";
const APP_PERMISSIONS = ["https://app.example.com/*"];

const manifest: ManifestV3 = {
  manifest_version: 3,
  permissions: ["cookies", "scripting", "declarativeNetRequest"],
  host_permissions: ["https://app.example.com/*", "https://bank.example/*"],
  content_scripts: [{ matches: ["<all_urls>"], js: ["content.js"] }],
  options_page: "options.html",
  web_accessible_resources: [{ resources: ["options.html"], matches: ["https://*.partner.example/*"] }],
};

const secureRoute: MessageRoute = {
  id: "read-cookie",
  from: "content-script",
  to: "service-worker",
  messageType: "cookies.read",
  actionId: "cookies.read",
  originCheck: { mode: "allowlist", patterns: APP_PERMISSIONS },
  senderBinding: "verified",
  tabPermissionBound: true,
  capabilityToken: {
    mode: "signed",
    boundTo: ["route", "origin", "tabId", "action", "permissions", "target"],
  },
  targetScope: "sender-origin",
  operation: { kind: "cookie-read", target: "https://app.example.com/account" },
};

function sender(overrides: Partial<Parameters<RuntimeMessageGate["authorize"]>[0]["sender"]> = {}) {
  return {
    origin: APP,
    tabId: 7,
    tabUrl: "https://app.example.com/page",
    tabPermissions: APP_PERMISSIONS,
    ...overrides,
  };
}

async function secureGate() {
  const authority = new CapabilityAuthority({ secret: SECRET });
  const gate = new RuntimeMessageGate({ manifest, routes: [secureRoute], authority });
  const token = await gate.issueToken("read-cookie", {
    origin: APP,
    tabId: 7,
    tabPermissions: APP_PERMISSIONS,
    target: "https://app.example.com/account",
  });
  return { gate, authority, token };
}

test("gate allows a fully bound same-origin cookie read", async () => {
  const { gate, token } = await secureGate();
  const decision = await gate.authorize({
    routeId: "read-cookie",
    messageType: "cookies.read",
    sender: sender(),
    token,
  });
  assert.equal(decision.allowed, true);
  assert.equal(decision.code, "ALLOWED");
  assert.equal(decision.tokenClaims?.origin, APP);
  assert.equal(decision.tokenClaims?.tabId, 7);
});

test("gate rejects missing, null and payload-spoofed sender origins", async () => {
  const { gate, token } = await secureGate();
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ origin: undefined }),
      token,
    })).code,
    "MISSING_SENDER_ORIGIN",
  );
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ origin: "null" }),
      token,
    })).code,
    "OPAQUE_SENDER_ORIGIN",
  );
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token,
      payload: { origin: ATTACKER },
    })).code,
    "PAYLOAD_ORIGIN_SPOOFED",
  );
});

test("gate enforces origin check and allowlist scoping", async () => {
  const { gate, token } = await secureGate();
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ origin: ATTACKER, tabUrl: `${ATTACKER}/page`, tabPermissions: [`${ATTACKER}/*`] }),
      token,
    })).code,
    "ORIGIN_SCOPE_VIOLATION",
  );

  const weak = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, originCheck: "none" }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await weak.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender(), token })).code,
    "ORIGIN_CHECK_MISSING",
  );

  const presence = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, originCheck: "presence-only" }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await presence.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender(), token })).code,
    "ORIGIN_CHECK_PRESENCE_ONLY",
  );
});

test("gate rejects payload-origin sender binding", async () => {
  const gate = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, senderBinding: "payload-origin" }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await gate.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender() })).code,
    "SENDER_BINDING_MISSING",
  );
});

test("gate requires and verifies an unforgeable bound capability token", async () => {
  const { gate, token } = await secureGate();
  assert.equal(
    (await gate.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender() })).code,
    "CAPABILITY_TOKEN_REQUIRED",
  );

  const tampered = `${token.slice(0, -1)}${token.endsWith("A") ? "B" : "A"}`;
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token: tampered,
    })).code,
    "CAPABILITY_TOKEN_INVALID",
  );
});

test("static tokens and incomplete token bindings are rejected", async () => {
  const staticGate = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, capabilityToken: { mode: "static", secretRef: "client-secret" } }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await staticGate.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender() })).code,
    "CAPABILITY_TOKEN_STATIC",
  );

  const incomplete = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, capabilityToken: { mode: "signed", boundTo: ["route", "origin"] } }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await incomplete.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender() })).code,
    "CAPABILITY_TOKEN_BINDING_INCOMPLETE",
  );
});

test("token is bound to route, action, origin, tab and live permission set", async () => {
  const { gate, authority } = await secureGate();
  const wrongAction = await authority.issueToken(
    {
      routeId: "read-cookie",
      actionId: "other-action",
      origin: APP,
      tabId: 7,
      tabPermissions: APP_PERMISSIONS,
      target: "https://app.example.com/account",
    },
    secureRoute,
  );
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token: wrongAction,
    })).code,
    "CAPABILITY_TOKEN_INVALID",
  );

  const wrongTab = await gate.issueToken("read-cookie", {
    origin: APP,
    tabId: 8,
    tabPermissions: APP_PERMISSIONS,
    target: "https://app.example.com/account",
  });
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token: wrongTab,
    })).code,
    "CAPABILITY_TOKEN_INVALID",
  );

  const wrongOrigin = await gate.issueToken("read-cookie", {
    origin: ATTACKER,
    tabId: 7,
    tabPermissions: [`${ATTACKER}/*`],
    target: "https://app.example.com/account",
  });
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ origin: ATTACKER, tabUrl: `${ATTACKER}/page`, tabPermissions: [`${ATTACKER}/*`] }),
      token: wrongOrigin,
    })).code,
    "ORIGIN_SCOPE_VIOLATION",
  );

  const wrongPermissions = await gate.issueToken("read-cookie", {
    origin: APP,
    tabId: 7,
    tabPermissions: APP_PERMISSIONS,
    target: "https://app.example.com/account",
  });
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ tabPermissions: [...APP_PERMISSIONS, "https://other.example/*"] }),
      token: wrongPermissions,
    })).code,
    "CAPABILITY_TOKEN_INVALID",
  );
});

test("tokens expire and reject stale issuance", async () => {
  let now = 1_000_000;
  const authority = new CapabilityAuthority({ secret: SECRET, clock: () => now });
  const gate = new RuntimeMessageGate({ manifest, routes: [secureRoute], authority });
  const token = await gate.issueToken("read-cookie", {
    origin: APP,
    tabId: 7,
    tabPermissions: APP_PERMISSIONS,
    target: "https://app.example.com/account",
    ttlMs: 100,
  });
  now += 101;
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token,
    })).code,
    "CAPABILITY_TOKEN_INVALID",
  );
});

test("cross-origin targets are denied even when the tab grant and token name them", async () => {
  const crossRoute: MessageRoute = {
    ...secureRoute,
    originCheck: { mode: "allowlist", patterns: ["*://*/*"] },
    operation: { kind: "cookie-read", target: "https://bank.example/account" },
    targetScope: "tab-permissions",
  };
  const authority = new CapabilityAuthority({ secret: SECRET });
  const gate = new RuntimeMessageGate({ manifest, routes: [crossRoute], authority });
  const token = await gate.issueToken("read-cookie", {
    origin: ATTACKER,
    tabId: 7,
    tabPermissions: [`${ATTACKER}/*`, "https://bank.example/*"],
    target: "https://bank.example/account",
  });
  const decision = await gate.authorize({
    routeId: "read-cookie",
    messageType: "cookies.read",
    sender: sender({
      origin: ATTACKER,
      tabUrl: `${ATTACKER}/page`,
      tabPermissions: [`${ATTACKER}/*`, "https://bank.example/*"],
    }),
    token,
  });
  assert.equal(decision.allowed, false);
  assert.equal(decision.code, "TARGET_OUTSIDE_ORIGIN_BOUNDARY");
});

test("wildcard-subdomain pattern targets are not treated as same-origin for injection", async () => {
  const injectionRoute: MessageRoute = {
    ...secureRoute,
    operation: {
      kind: "script-injection",
      target: "https://*.app.example.com/*",
      targetKind: "pattern",
    },
    targetScope: "sender-origin",
  };
  const authority = new CapabilityAuthority({ secret: SECRET });
  const gate = new RuntimeMessageGate({
    manifest: {
      ...manifest,
      permissions: ["scripting"],
      host_permissions: ["<all_urls>"],
    },
    routes: [injectionRoute],
    authority,
  });
  const token = await gate.issueToken("read-cookie", {
    origin: APP,
    tabId: 7,
    tabPermissions: APP_PERMISSIONS,
    target: "https://*.app.example.com/*",
  });
  const decision = await gate.authorize({
    routeId: "read-cookie",
    messageType: "cookies.read",
    sender: sender(),
    token,
  });
  assert.equal(decision.allowed, false);
  assert.equal(decision.code, "TARGET_OUTSIDE_ORIGIN_BOUNDARY");
});

test("target must be covered by tab permissions and manifest privileges", async () => {
  const crossRoute: MessageRoute = {
    ...secureRoute,
    originCheck: { mode: "allowlist", patterns: ["*://*/*"] },
    operation: { kind: "cookie-read", target: "https://bank.example/account" },
    targetScope: "tab-permissions",
  };
  const authority = new CapabilityAuthority({ secret: SECRET });
  const gate = new RuntimeMessageGate({ manifest, routes: [crossRoute], authority });
  const token = await gate.issueToken("read-cookie", {
    origin: ATTACKER,
    tabId: 7,
    tabPermissions: [`${ATTACKER}/*`],
    target: "https://bank.example/account",
  });
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ origin: ATTACKER, tabUrl: `${ATTACKER}/page`, tabPermissions: [`${ATTACKER}/*`] }),
      token,
    })).code,
    "TARGET_NOT_IN_TAB_PERMISSIONS",
  );

  const noCookies: ManifestV3 = {
    ...manifest,
    permissions: ["scripting"],
  };
  const noCookiesGate = new RuntimeMessageGate({ manifest: noCookies, routes: [secureRoute], authority });
  const appToken = await noCookiesGate.issueToken("read-cookie", {
    origin: APP,
    tabId: 7,
    tabPermissions: APP_PERMISSIONS,
    target: "https://app.example.com/account",
  });
  assert.equal(
    (await noCookiesGate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender(),
      token: appToken,
    })).code,
    "OPERATION_NOT_GRANTED",
  );
});

test("gate rejects missing tab context and unbound tab permission set", async () => {
  const { gate, token } = await secureGate();
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ tabId: undefined }),
      token,
    })).code,
    "MISSING_TAB_CONTEXT",
  );
  assert.equal(
    (await gate.authorize({
      routeId: "read-cookie",
      messageType: "cookies.read",
      sender: sender({ tabPermissions: [] }),
      token,
    })).code,
    "MISSING_TAB_CONTEXT",
  );

  const unbound = new RuntimeMessageGate({
    manifest,
    routes: [{ ...secureRoute, tabPermissionBound: false }],
    authority: new CapabilityAuthority({ secret: SECRET }),
  });
  assert.equal(
    (await unbound.authorize({ routeId: "read-cookie", messageType: "cookies.read", sender: sender(), token })).code,
    "TAB_PERMISSION_NOT_BOUND",
  );
});

test("static review reports missing origin, binding, token and target scope", () => {
  const route: MessageRoute = {
    id: "bad-forward",
    from: "content-script",
    to: "service-worker",
    messageType: "cookies.read",
    expectedPageOrigins: [ATTACKER],
    originCheck: "none",
    senderBinding: "none",
    tabPermissionBound: false,
    capabilityToken: { mode: "static" },
    targetScope: "none",
    operation: { kind: "cookie-read", target: "https://bank.example/account" },
  };
  const codes = verifyMessageRouteSecurity(route).map((issue) => issue.code);
  assert.equal(codes.includes("MISSING_ORIGIN_CHECK"), true);
  assert.equal(codes.includes("SENDER_ORIGIN_UNBOUND"), true);
  assert.equal(codes.includes("TAB_PERMISSION_NOT_BOUND"), true);
  assert.equal(codes.includes("CAPABILITY_TOKEN_STATIC"), true);
  assert.equal(codes.includes("TARGET_SCOPE_MISSING"), true);
  assert.equal(codes.includes("TARGET_OUTSIDE_TAB_ORIGIN"), true);
  assert.equal(isRouteSafe(route), false);
});

test("manifest-level review flags WAR clickjacking and DNR priority overrides", () => {
  const issues = analyzeManifestSecurity(manifest, [], [
    { id: 1, priority: 1, action: { type: "block" }, condition: { urlFilter: "*" } },
    { id: 2, priority: 1, action: { type: "allowAllRequests" }, condition: { urlFilter: "*" } },
  ]);
  assert.equal(issues.some((issue) => issue.code === "WAR_PRIVILEGED_PAGE_EXPOSED"), true);
  assert.equal(issues.some((issue) => issue.code === "DNR_ALLOW_ALL_REQUESTS_OVERRIDE"), true);

  const exposed = {
    ...manifest,
    web_accessible_resources: [{ resources: ["options.html"], matches: ["<all_urls>"] }],
  };
  assert.equal(
    analyzeManifestSecurity(exposed, []).some((issue) => issue.code === "WAR_PRIVILEGED_PAGE_EXPOSED"),
    true,
  );
  assert.equal(
    reviewRuntimeSecurity({ manifest: exposed, routes: [] }).safe,
    false,
  );
});

test("externally connectable wildcard is flagged", () => {
  const wildcard: ManifestV3 = {
    ...manifest,
    externally_connectable: { matches: ["<all_urls>"] },
  };
  const issues = analyzeManifestSecurity(wildcard, [secureRoute]);
  assert.equal(issues.some((issue) => issue.code === "EXTERNALLY_CONNECTABLE_WILDCARD"), true);
  assert.equal(issues.some((issue) => issue.code === "EXTERNALLY_CONNECTABLE_UNSCOPED"), true);
});
