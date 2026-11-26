import test from "node:test";
import assert from "node:assert/strict";
import {
  InvalidManifestError,
  externallyConnectableMatchesOrigin,
  findWarClickjackingFindings,
  isLikelyPrivilegedExtensionPage,
  parseManifest,
  privilegedPageResources,
  solveManifestPermissions,
  validateManifest,
} from "../src/manifest.ts";

const extensionId = "a".repeat(32);

test("parses MV3 JSON and solves mandatory, optional and host permissions", () => {
  const manifest = parseManifest(
    JSON.stringify({
      manifest_version: 3,
      permissions: ["cookies", "storage"],
      optional_permissions: ["scripting"],
      host_permissions: ["https://app.example.com/*"],
      optional_host_permissions: ["https://bank.example/*"],
      content_scripts: [{ matches: ["https://app.example.com/*"], js: ["content.js"] }],
    }),
  );

  const solution = solveManifestPermissions(manifest, {
    grantedOptionalPermissions: ["scripting"],
    grantedOptionalHostPermissions: ["https://bank.example/*"],
  });
  assert.deepEqual(solution.apiPermissions, ["cookies", "storage", "scripting"]);
  assert.equal(solution.hasApiPermission("cookies"), true);
  assert.equal(solution.hasApiPermission("scripting"), true);
  assert.equal(solution.hasApiPermission("tabs"), false);
  assert.equal(solution.apiPermissionStatus("scripting").optional, true);
  assert.equal(solution.isHostPermitted("https://app.example.com/account"), true);
  assert.equal(solution.isHostPermitted("https://bank.example/account"), true);
  assert.equal(solution.isHostPermitted("https://evil.example/account"), false);
  assert.equal(solution.isContentScriptInjected("https://app.example.com/page"), true);
  assert.equal(solution.isContentScriptInjected("https://other.example/page"), false);
  assert.deepEqual(solution.accessProfile.powerfulApis, ["cookies", "scripting"]);
});

test("optional host permission stays ungranted until explicitly granted", () => {
  const manifest = {
    manifest_version: 3 as const,
    host_permissions: ["https://app.example.com/*"],
    optional_host_permissions: ["https://bank.example/*"],
  };
  const ungranted = solveManifestPermissions(manifest);
  assert.equal(ungranted.isHostPermitted("https://bank.example/account"), false);
  assert.equal(ungranted.hostPermissions.find((entry) => entry.pattern.includes("bank"))?.granted, false);

  const granted = solveManifestPermissions(manifest, {
    grantedOptionalHostPermissions: ["https://bank.example/*"],
  });
  assert.equal(granted.isHostPermitted("https://bank.example/account"), true);
});

test("content scripts use include minus exclude matches", () => {
  const solution = solveManifestPermissions({
    manifest_version: 3,
    content_scripts: [
      {
        matches: ["<all_urls>"],
        exclude_matches: ["https://bank.example/*", "https://*.internal.example/*"],
        js: ["content.js"],
      },
    ],
  });
  assert.equal(solution.isContentScriptInjected("https://public.example/page"), true);
  assert.equal(solution.isContentScriptInjected("https://bank.example/account"), false);
  assert.equal(solution.isContentScriptInjected("https://app.internal.example/page"), false);
});

test("WAR exposure is origin-scoped and clickjacking findings identify privileged pages", () => {
  const manifest = {
    manifest_version: 3 as const,
    options_page: "options.html",
    action: { default_popup: "/popup.html" },
    web_accessible_resources: [
      {
        resources: ["options.html", "popup.html"],
        matches: ["https://*.partner.example/*"],
      },
      {
        resources: ["images/*"],
        matches: ["<all_urls>"],
      },
    ],
  };
  const solution = solveManifestPermissions(manifest);
  assert.deepEqual(solution.privilegedPages, ["options.html", "popup.html"]);
  assert.equal(solution.isWebAccessibleForOrigin("options.html", "https://app.partner.example"), true);
  assert.equal(solution.isWebAccessibleForOrigin("options.html", "https://evil.example"), false);
  assert.equal(solution.isWebAccessibleForOrigin("images/logo.png", "https://evil.example"), true);

  const findings = findWarClickjackingFindings(manifest);
  assert.equal(findings.length, 2);
  assert.equal(findings.every((finding) => finding.severity === "high"), true);
  assert.deepEqual(findings.map((finding) => finding.privilegedPage).sort(), ["options.html", "popup.html"]);
});

test("WAR that exposes privileged pages to <all_urls> is critical", () => {
  const manifest = {
    manifest_version: 3 as const,
    options_ui: { page: "settings/index.html" },
    web_accessible_resources: [{ resources: ["*.html"], matches: ["<all_urls>"] }],
  };
  const findings = findWarClickjackingFindings(manifest);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].privilegedPage, "settings/index.html");
  assert.equal(findings[0].exposedToAllUrls, true);
  assert.equal(findings[0].severity, "critical");
});

test("WAR extension_ids do not grant web exposure", () => {
  const manifest = {
    manifest_version: 3 as const,
    web_accessible_resources: [{ resources: ["lib.js"], extension_ids: [extensionId] }],
  };
  const solution = solveManifestPermissions(manifest);
  assert.equal(solution.isWebAccessibleForOrigin("lib.js", extensionId), true);
  assert.equal(solution.isWebAccessibleForOrigin("lib.js", "https://evil.example"), false);
});

test("likely internal HTML is flagged even without a manifest page declaration", () => {
  const manifest = {
    manifest_version: 3 as const,
    web_accessible_resources: [{ resources: ["index.html"], matches: ["<all_urls>"] }],
  };
  const findings = findWarClickjackingFindings(manifest);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].privilegedPage, "index.html");
  assert.equal(findings[0].severity, "critical");
});

test("externally_connectable matches web origins and extension ids", () => {
  const manifest = {
    manifest_version: 3 as const,
    externally_connectable: {
      matches: ["https://*.partner.example/*"],
      ids: [extensionId],
    },
  };
  assert.equal(externallyConnectableMatchesOrigin(manifest, "https://app.partner.example"), true);
  assert.equal(externallyConnectableMatchesOrigin(manifest, "https://evil.example"), false);
  assert.equal(externallyConnectableMatchesOrigin(manifest, extensionId), true);
  assert.equal(
    externallyConnectableMatchesOrigin({ manifest_version: 3 }, "https://evil.example"),
    false,
  );
});

test("declarativeNetRequest rule resources are validated and exposed", () => {
  const manifest = {
    manifest_version: 3 as const,
    permissions: ["declarativeNetRequest"],
    declarative_net_request: {
      rule_resources: [
        { id: "enterprise", enabled: true, path: "rules/enterprise.json" },
        { id: "telemetry", enabled: false, path: "rules/telemetry.json" },
      ],
    },
  };
  const solution = solveManifestPermissions(manifest);
  assert.equal(solution.dnrRuleResources.length, 2);
  assert.deepEqual(solution.dnrRuleResources.map((resource) => resource.id), ["enterprise", "telemetry"]);
});

test("privileged page heuristics avoid obvious public assets", () => {
  assert.equal(isLikelyPrivilegedExtensionPage("options.html"), true);
  assert.equal(isLikelyPrivilegedExtensionPage("sidepanel/index.html"), true);
  assert.equal(isLikelyPrivilegedExtensionPage("public/widget.html"), false);
  assert.equal(isLikelyPrivilegedExtensionPage("web_accessible/popup.html"), false);
  assert.equal(isLikelyPrivilegedExtensionPage("images/logo.png"), false);
  assert.deepEqual(privilegedPageResources({ manifest_version: 3 }), []);
});

test("invalid MV3 manifests are rejected with actionable issues", () => {
  const badManifests: unknown[] = [
    { manifest_version: 2 },
    {
      manifest_version: 3,
      permissions: ["<all_urls>"],
    },
    {
      manifest_version: 3,
      content_scripts: [{ js: ["content.js"] }],
    },
    {
      manifest_version: 3,
      web_accessible_resources: ["options.html"],
    },
    {
      manifest_version: 3,
      web_accessible_resources: [{ resources: ["options.html"] }],
    },
    {
      manifest_version: 3,
      host_permissions: ["https://example.com"],
    },
    {
      manifest_version: 3,
      declarative_net_request: {
        rule_resources: [
          { id: "same", enabled: true, path: "a.json" },
          { id: "same", enabled: true, path: "b.json" },
        ],
      },
    },
  ];
  for (const manifest of badManifests) {
    const issues = validateManifest(manifest);
    assert.equal(issues.some((issue) => issue.severity !== "info"), true, JSON.stringify(manifest));
    assert.throws(() => parseManifest(manifest), InvalidManifestError, JSON.stringify(manifest));
  }
  assert.throws(() => parseManifest("{not json"), InvalidManifestError);
});

test("unknown API permissions are informational but parseable", () => {
  const issues = validateManifest({ manifest_version: 3, permissions: ["futureApi"] });
  assert.equal(issues.length, 1);
  assert.equal(issues[0].severity, "info");
  assert.equal(parseManifest({ manifest_version: 3, permissions: ["futureApi"] }).manifest_version, 3);
});
