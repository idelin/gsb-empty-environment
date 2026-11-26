import test from "node:test";
import assert from "node:assert/strict";
import { analyzeSecurity, searchConfusedDeputyPaths } from "../src/index.ts";
import type { ManifestV3, MessageRoute } from "../src/index.ts";

const ATTACKER = "https://attacker.example";
const APP = "https://app.example.com";
const BANK = "https://bank.example";

const openManifest: ManifestV3 = {
  manifest_version: 3,
  permissions: ["cookies", "scripting", "declarativeNetRequest", "storage"],
  host_permissions: ["<all_urls>"],
  content_scripts: [{ matches: ["<all_urls>"], js: ["content.js"] }],
  externally_connectable: { matches: ["<all_urls>"] },
};

const pageToContent: MessageRoute = {
  id: "page-to-content",
  from: "page",
  to: "content-script",
  messageType: "forward",
  originCheck: "none",
};

const contentToWorker: MessageRoute = {
  id: "content-to-worker",
  from: "content-script",
  to: "service-worker",
  messageType: "cookies.read",
  originCheck: "same-origin",
  senderBinding: "verified",
  tabPermissionBound: true,
  capabilityToken: { mode: "none" },
  targetScope: "none",
  operation: { kind: "cookie-read", target: BANK, targetKind: "domain" },
};

test("finds the minimal two-hop page->content->worker cookie path", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [pageToContent, contentToWorker],
  });
  assert.equal(result.paths.length, 1);
  assert.equal(result.bestPath?.hopCount, 2);
  assert.equal(result.bestPath?.entryOrigin, ATTACKER);
  assert.equal(result.bestPath?.sink.kind, "cookie-read");
  assert.equal(result.bestPath?.sinkRouteId, "content-to-worker");
  assert.deepEqual(
    result.bestPath?.hops.filter((hop) => !hop.routeId.startsWith("war:")).map((hop) => hop.routeId),
    ["page-to-content", "content-to-worker"],
  );
  assert.equal(result.bestPath?.missingControls.includes("origin-check-missing"), true);
  assert.equal(result.bestPath?.missingControls.includes("target-scope-missing"), true);
});

test("respects 1-4 hop bound and rejects larger bounds", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [pageToContent, contentToWorker],
    maxHops: 1,
  });
  assert.equal(result.paths.length, 0);
  assert.equal(result.maxHops, 1);
  assert.throws(
    () => searchConfusedDeputyPaths({ manifest: openManifest, routes: [], maxHops: 5 }),
    RangeError,
  );
});

test("origin allowlist for a trusted site blocks an untrusted entry", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      { ...pageToContent, originCheck: { mode: "allowlist", patterns: ["https://trusted.example/*"] } },
      contentToWorker,
    ],
  });
  assert.equal(result.paths.length, 0);
});

test("sender-origin target scope blocks cross-origin sink after verified binding", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      pageToContent,
      { ...contentToWorker, targetScope: "sender-origin" },
    ],
  });
  assert.equal(result.paths.length, 0);
});

test("full signed token binding hardens the route even with weak origin checks", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      pageToContent,
      {
        ...contentToWorker,
        capabilityToken: {
          mode: "signed",
          boundTo: ["route", "origin", "tabId", "action", "permissions", "target"],
        },
      },
    ],
  });
  assert.equal(result.paths.length, 0);
});

test("same-origin cookie targets are not sensitive confused-deputy sinks", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      pageToContent,
      {
        ...contentToWorker,
        operation: { kind: "cookie-read", target: `${ATTACKER}/account` },
      },
    ],
  });
  assert.equal(result.paths.length, 0);
});

test("missing manifest privileges suppress false-positive paths", () => {
  const noCookies: ManifestV3 = {
    ...openManifest,
    permissions: ["scripting"],
  };
  const result = searchConfusedDeputyPaths({ manifest: noCookies, routes: [pageToContent, contentToWorker] });
  assert.equal(result.paths.length, 0);
});

test("static token is treated as ineffective and still yields a path", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      pageToContent,
      {
        ...contentToWorker,
        capabilityToken: { mode: "static", secretRef: "bundled-secret" },
        targetScope: "none",
      },
    ],
  });
  assert.equal(result.paths.length, 1);
  assert.equal(result.bestPath?.missingControls.includes("capability-token-static"), true);
});

test("target scope tab-permissions blocks cross-origin access outside the live grant", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      pageToContent,
      { ...contentToWorker, targetScope: "tab-permissions" },
    ],
  });
  assert.equal(result.paths.length, 0);
});

test("finds a three-hop scripting path", () => {
  const injection: MessageRoute = {
    id: "inject-script",
    from: "service-worker",
    to: "service-worker",
    messageType: "script.inject",
    fromHandler: "cookie-worker",
    toHandler: "inject",
    originCheck: "none",
    tabPermissionBound: true,
    capabilityToken: { mode: "none" },
    targetScope: "none",
    operation: { kind: "script-injection", target: "https://bank.example/account" },
  };
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      { ...pageToContent, toHandler: "content-forward" },
      { ...contentToWorker, fromHandler: "content-forward", toHandler: "cookie-worker" },
      injection,
    ],
    maxPaths: 10,
  });
  const injectionPath = result.paths.find((path) => path.sink.kind === "script-injection");
  assert.equal(injectionPath?.hopCount, 3);
  assert.deepEqual(
    injectionPath?.hops.filter((hop) => !hop.routeId.startsWith("war:")).map((hop) => hop.routeId),
    ["page-to-content", "content-to-worker", "inject-script"],
  );
});

test("handler routing can represent a four-hop scripting path", () => {
  const bridge: MessageRoute = {
    id: "worker-to-extension-page",
    from: "service-worker",
    to: "extension-page",
    messageType: "bridge",
    fromHandler: "cookie-worker",
    toHandler: "extension-page-handler",
    senderBinding: "none",
  };
  const injection: MessageRoute = {
    id: "extension-page-inject",
    from: "extension-page",
    to: "service-worker",
    messageType: "script.inject",
    fromHandler: "extension-page-handler",
    toHandler: "inject",
    tabPermissionBound: true,
    capabilityToken: { mode: "none" },
    targetScope: "none",
    operation: { kind: "script-injection", target: "https://bank.example/account" },
  };
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [
      { ...pageToContent, toHandler: "content-forward" },
      { ...contentToWorker, fromHandler: "content-forward", toHandler: "cookie-worker" },
      bridge,
      injection,
    ],
    maxPaths: 20,
  });
  const injectionPath = result.paths.find((path) => path.sink.kind === "script-injection");
  assert.equal(injectionPath?.hopCount, 4);
  assert.deepEqual(
    injectionPath?.hops.filter((hop) => !hop.routeId.startsWith("war:")).map((hop) => hop.routeId),
    ["page-to-content", "content-to-worker", "worker-to-extension-page", "extension-page-inject"],
  );
});

test("a five-hop chain is not reported when maxHops is four", () => {
  const chain: MessageRoute[] = [
    { ...pageToContent, toHandler: "content-forward" },
    { ...contentToWorker, fromHandler: "content-forward", toHandler: "cookie-worker" },
    {
      id: "hop-3",
      from: "service-worker",
      to: "service-worker",
      messageType: "bridge",
      fromHandler: "cookie-worker",
      toHandler: "handler-3",
      senderBinding: "none",
    },
    {
      id: "hop-4",
      from: "service-worker",
      to: "service-worker",
      messageType: "bridge",
      fromHandler: "handler-3",
      toHandler: "handler-4",
      senderBinding: "none",
    },
    {
      id: "hop-5",
      from: "service-worker",
      to: "service-worker",
      messageType: "script.inject",
      fromHandler: "handler-4",
      toHandler: "inject",
      tabPermissionBound: true,
      capabilityToken: { mode: "none" },
      targetScope: "none",
      operation: { kind: "script-injection", target: "https://bank.example/account" },
    },
  ];
  const result = searchConfusedDeputyPaths({ manifest: openManifest, routes: chain, maxHops: 4, maxPaths: 20 });
  assert.equal(result.paths.some((path) => path.sink.kind === "script-injection"), false);
});

test("externally_connectable direct page->worker route is one hop", () => {
  const direct: MessageRoute = {
    id: "external-read",
    from: "page",
    to: "service-worker",
    messageType: "cookies.read",
    originCheck: "same-origin",
    senderBinding: "verified",
    tabPermissionBound: true,
    capabilityToken: { mode: "none" },
    targetScope: "none",
    operation: { kind: "cookie-read", target: BANK, targetKind: "domain" },
  };
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [direct],
    entries: [{ pageOrigin: ATTACKER, via: "externally-connectable", messageType: "cookies.read" }],
  });
  assert.equal(result.bestPath?.hopCount, 1);

  const noExternal: ManifestV3 = {
    ...openManifest,
    externally_connectable: undefined,
  };
  const blocked = searchConfusedDeputyPaths({
    manifest: noExternal,
    routes: [direct],
    entries: [{ pageOrigin: ATTACKER, via: "externally-connectable", messageType: "cookies.read" }],
  });
  assert.equal(blocked.paths.length, 0);
});

test("WAR-exposed privileged extension page is a reachable entry", () => {
  const exposed: ManifestV3 = {
    ...openManifest,
    options_page: "options.html",
    web_accessible_resources: [{ resources: ["options.html"], matches: ["<all_urls>"] }],
  };
  const internalBridge: MessageRoute = {
    id: "options-to-worker",
    from: "extension-page",
    to: "service-worker",
    messageType: "cookies.read",
    tabPermissionBound: true,
    capabilityToken: { mode: "none" },
    targetScope: "none",
    operation: { kind: "cookie-read", target: BANK, targetKind: "domain" },
  };
  const result = searchConfusedDeputyPaths({
    manifest: exposed,
    routes: [internalBridge],
    entries: [{ pageOrigin: ATTACKER, via: "web-accessible-resource", resource: "options.html" }],
  });
  assert.equal(result.paths.length, 1);
  assert.equal(result.bestPath?.hopCount, 1);
  assert.equal(result.bestPath?.missingControls.includes("war-privileged-page-exposed"), true);
  assert.equal(result.bestPath?.hops[0].routeId, "war:options.html");

  const scoped: ManifestV3 = {
    ...exposed,
    web_accessible_resources: [{ resources: ["options.html"], matches: ["https://trusted.example/*"] }],
  };
  assert.equal(
    searchConfusedDeputyPaths({
      manifest: scoped,
      routes: [internalBridge],
      entries: [{ pageOrigin: ATTACKER, via: "web-accessible-resource", resource: "options.html" }],
    }).paths.length,
    0,
  );
});

test("DNR bypass and fetch proxy sinks are found with proper severity", () => {
  const dnr: MessageRoute = {
    id: "dnr-bypass",
    from: "content-script",
    to: "service-worker",
    messageType: "dnr.bypass",
    originCheck: "same-origin",
    senderBinding: "verified",
    tabPermissionBound: true,
    capabilityToken: { mode: "none" },
    targetScope: "none",
    operation: { kind: "dnr-bypass", target: "https://bank.example/*", targetKind: "pattern" },
  };
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [pageToContent, dnr],
  });
  assert.equal(result.bestPath?.sink.kind, "dnr-bypass");
  assert.equal(result.bestPath?.severity, "critical");
});

test("analyzeSecurity merges static findings and attack paths", () => {
  const result = analyzeSecurity({
    manifest: {
      ...openManifest,
      options_page: "options.html",
      web_accessible_resources: [{ resources: ["options.html"], matches: ["<all_urls>"] }],
    },
    routes: [pageToContent, contentToWorker],
    dnrRules: [
      { id: 1, priority: 1, action: { type: "block" }, condition: { urlFilter: "*" } },
      { id: 2, priority: 1, action: { type: "allowAllRequests" }, condition: { urlFilter: "*" } },
    ],
  });
  assert.equal(result.safe, false);
  assert.equal(result.attackPaths.length, 1);
  assert.equal(result.issues.some((issue) => issue.code === "CONFUSED_DEPUTY_PATH"), true);
  assert.equal(result.issues.some((issue) => issue.code === "MISSING_ORIGIN_CHECK"), true);
  assert.equal(result.issues.some((issue) => issue.code === "WAR_PRIVILEGED_PAGE_EXPOSED"), true);
  assert.equal(result.issues.some((issue) => issue.code === "DNR_ALLOW_ALL_REQUESTS_OVERRIDE"), true);
});

test("explicit message type filters the reachable entry hop", () => {
  const result = searchConfusedDeputyPaths({
    manifest: openManifest,
    routes: [pageToContent, contentToWorker],
    entries: [{ pageOrigin: ATTACKER, via: "content-script", messageType: "not-forwarded" }],
  });
  assert.equal(result.paths.length, 0);
});
