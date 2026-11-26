import test from "node:test";
import assert from "node:assert/strict";
import { analyzeSecurity } from "../src/index.ts";
import type { ManifestV3, MessageRoute } from "../src/index.ts";

const ATTACKER = "https://attacker.example";

const baseManifest: ManifestV3 = {
  manifest_version: 3,
  permissions: ["cookies", "scripting", "declarativeNetRequest"],
  host_permissions: ["<all_urls>"],
  content_scripts: [{ matches: ["<all_urls>"], js: ["content.js"] }],
};

const pageEntry: MessageRoute = {
  id: "entry",
  from: "page",
  to: "content-script",
  messageType: "forward",
  originCheck: "none",
  toHandler: "content-handler",
};

const vulnerableRead: MessageRoute = {
  id: "read",
  from: "content-script",
  to: "service-worker",
  messageType: "cookies.read",
  fromHandler: "content-handler",
  toHandler: "cookie-handler",
  originCheck: "same-origin",
  senderBinding: "verified",
  tabPermissionBound: true,
  capabilityToken: { mode: "none" },
  targetScope: "none",
  operation: { kind: "cookie-read", target: "https://bank.example/account" },
};

interface MatrixCase {
  name: string;
  manifest: ManifestV3;
  routes: MessageRoute[];
  entries?: Parameters<typeof analyzeSecurity>[0]["entries"];
  expectPath: boolean;
  requireCodes?: string[];
  forbidCodes?: string[];
}

const cases: MatrixCase[] = [
  {
    name: "vulnerable content-script forward and cookie sink",
    manifest: baseManifest,
    routes: [pageEntry, vulnerableRead],
    expectPath: true,
    requireCodes: [
      "MISSING_ORIGIN_CHECK",
      "CAPABILITY_TOKEN_MISSING",
      "TARGET_SCOPE_MISSING",
      "CONFUSED_DEPUTY_PATH",
    ],
  },
  {
    name: "trusted origin allowlist excludes attacker",
    manifest: baseManifest,
    routes: [
      { ...pageEntry, originCheck: { mode: "allowlist", patterns: ["https://trusted.example/*"] } },
      vulnerableRead,
    ],
    expectPath: false,
  },
  {
    name: "same-origin cookie target is not a cross-site sink",
    manifest: baseManifest,
    routes: [
      pageEntry,
      { ...vulnerableRead, operation: { kind: "cookie-read", target: `${ATTACKER}/account` } },
    ],
    expectPath: false,
  },
  {
    name: "manifest without cookies permission cannot read cookies",
    manifest: { ...baseManifest, permissions: ["scripting", "declarativeNetRequest"] },
    routes: [pageEntry, vulnerableRead],
    expectPath: false,
  },
  {
    name: "fully-bound signed token closes cross-origin sink",
    manifest: baseManifest,
    routes: [
      pageEntry,
      {
        ...vulnerableRead,
        capabilityToken: {
          mode: "signed",
          boundTo: ["route", "origin", "tabId", "action", "permissions", "target"],
        },
      },
    ],
    expectPath: false,
  },
  {
    name: "tab-permission target scope blocks cross-origin bank target",
    manifest: baseManifest,
    routes: [pageEntry, { ...vulnerableRead, targetScope: "tab-permissions" }],
    expectPath: false,
  },
  {
    name: "WAR-exposed internal page is web reachable",
    manifest: {
      ...baseManifest,
      options_page: "options.html",
      web_accessible_resources: [{ resources: ["options.html"], matches: ["<all_urls>"] }],
    },
    routes: [
      {
        id: "internal-read",
        from: "extension-page",
        to: "service-worker",
        messageType: "cookies.read",
        tabPermissionBound: true,
        capabilityToken: { mode: "none" },
        targetScope: "none",
        operation: { kind: "cookie-read", target: "https://bank.example/account" },
      },
    ],
    entries: [{ pageOrigin: ATTACKER, via: "web-accessible-resource", resource: "options.html" }],
    expectPath: true,
    requireCodes: ["WAR_PRIVILEGED_PAGE_EXPOSED", "CONFUSED_DEPUTY_PATH"],
  },
  {
    name: "externally connectable direct worker message",
    manifest: { ...baseManifest, externally_connectable: { matches: ["<all_urls>"] } },
    routes: [
      {
        id: "external-read",
        from: "page",
        to: "service-worker",
        messageType: "cookies.read",
        originCheck: "same-origin",
        senderBinding: "verified",
        tabPermissionBound: true,
        capabilityToken: { mode: "none" },
        targetScope: "none",
        operation: { kind: "cookie-read", target: "https://bank.example/account" },
      },
    ],
    entries: [{ pageOrigin: ATTACKER, via: "externally-connectable", messageType: "cookies.read" }],
    expectPath: true,
    requireCodes: ["EXTERNALLY_CONNECTABLE_WILDCARD", "CONFUSED_DEPUTY_PATH"],
  },
  {
    name: "DNR allowAllRequests override is reported and bypass sink is reachable",
    manifest: baseManifest,
    routes: [
      pageEntry,
      {
        ...vulnerableRead,
        id: "dnr",
        messageType: "dnr.bypass",
        operation: { kind: "dnr-bypass", target: "https://bank.example/*", targetKind: "pattern" },
      },
    ],
    expectPath: true,
    requireCodes: ["DNR_ALLOW_ALL_REQUESTS_OVERRIDE", "CONFUSED_DEPUTY_PATH"],
  },
  {
    name: "five-hop handler chain is outside the bounded search",
    manifest: baseManifest,
    routes: [
      pageEntry,
      {
        ...vulnerableRead,
        id: "hop-2",
        messageType: "bridge",
        fromHandler: "content-handler",
        toHandler: "h2",
        operation: undefined,
      },
      {
        id: "hop-3",
        from: "service-worker",
        to: "service-worker",
        messageType: "bridge",
        fromHandler: "h2",
        toHandler: "h3",
      },
      {
        id: "hop-4",
        from: "service-worker",
        to: "service-worker",
        messageType: "bridge",
        fromHandler: "h3",
        toHandler: "h4",
      },
      {
        id: "hop-5",
        from: "service-worker",
        to: "service-worker",
        messageType: "cookies.read",
        fromHandler: "h4",
        toHandler: "h5",
        tabPermissionBound: true,
        capabilityToken: { mode: "none" },
        targetScope: "none",
        operation: { kind: "cookie-read", target: "https://bank.example/account" },
      },
    ],
    expectPath: false,
  },
  {
    name: "presence-only origin check is a static finding",
    manifest: baseManifest,
    routes: [{ ...pageEntry, originCheck: "presence-only" }, vulnerableRead],
    expectPath: true,
    requireCodes: ["ORIGIN_CHECK_PRESENCE_ONLY", "CONFUSED_DEPUTY_PATH"],
  },
];

for (const matrixCase of cases) {
  test(`scenario matrix: ${matrixCase.name}`, () => {
    const dnrRules =
      matrixCase.requireCodes?.includes("DNR_ALLOW_ALL_REQUESTS_OVERRIDE") === true
        ? [
            { id: 1, priority: 1, action: { type: "block" }, condition: { urlFilter: "*" } },
            {
              id: 2,
              priority: 1,
              action: { type: "allowAllRequests" },
              condition: { urlFilter: "*" },
            },
          ]
        : undefined;
    const result = analyzeSecurity({
      manifest: matrixCase.manifest,
      routes: matrixCase.routes,
      entries: matrixCase.entries,
      dnrRules,
    });
    assert.equal(result.attackPaths.length > 0, matrixCase.expectPath, matrixCase.name);
    for (const code of matrixCase.requireCodes ?? []) {
      assert.equal(result.issues.some((issue) => issue.code === code), true, `${matrixCase.name}: ${code}`);
    }
    for (const code of matrixCase.forbidCodes ?? []) {
      assert.equal(result.issues.some((issue) => issue.code === code), false, `${matrixCase.name}: ${code}`);
    }
  });
}
