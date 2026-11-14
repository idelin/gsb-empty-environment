import assert from "node:assert/strict";
import test from "node:test";
import {
  CONFIG_MESSAGE_TYPE,
  loadManifestConfig,
  narrowConfig,
  parseTrustedConfigMessage,
} from "../src/config.ts";
import {
  ValidationError,
  type ValidationCode,
} from "../src/security.ts";
import {
  extensionManifest,
  FakeRuntime,
} from "./helpers.ts";
import { isTrustedExtensionSender } from "../src/content_script.ts";

function assertCode(code: ValidationCode, operation: () => unknown): void {
  assert.throws(operation, (error: unknown) => {
    return error instanceof ValidationError && error.code === code;
  });
}

test("manifest origins are frozen and replace page-derived configuration", () => {
  const config = loadManifestConfig(
    extensionManifest(["https://api.example.com", "https://cdn.example.com"]),
  );

  assert.deepEqual(config.allowedOrigins, [
    "https://api.example.com",
    "https://cdn.example.com",
  ]);
  assert.equal(Object.isFrozen(config), true);
  assert.equal(Object.isFrozen(config.allowedOrigins), true);
  assert.equal(Object.isFrozen(config.allowedOperations), true);
});

test("manifest cannot widen host_permissions or use wildcard hosts", () => {
  assertCode("not_allowed", () =>
    loadManifestConfig(
      extensionManifest(["https://api.example.com"], {
        allowedOrigins: ["https://evil.example"],
      }),
    ),
  );

  assertCode("invalid_value", () =>
    loadManifestConfig({
      manifest_version: 3,
      host_permissions: ["https://*.example.com/*"],
    }),
  );
});

test("manifest and trusted messages reject prototype keys and unknown fields", () => {
  const manifest = JSON.parse(
    JSON.stringify({
      manifest_version: 3,
      host_permissions: ["https://api.example.com/*"],
      extension_security: {
        version: 1,
        allowedOrigins: ["https://api.example.com"],
      },
    }),
  ) as Record<string, unknown>;
  const security = manifest.extension_security as Record<string, unknown>;
  Object.defineProperty(security, "__proto__", {
    value: { allowedOrigins: ["https://evil.example"] },
    enumerable: true,
  });
  assertCode("prototype_key", () => loadManifestConfig(manifest));

  const config = loadManifestConfig(extensionManifest());
  assertCode("prototype_key", () =>
    parseTrustedConfigMessage(
      JSON.parse(
        `{"type":"${CONFIG_MESSAGE_TYPE}","protocolVersion":1,"config":{"__proto__":{"allowedOrigins":["https://evil.example"]}}}`,
      ),
      config,
    ),
  );
  assertCode("unknown_key", () =>
    parseTrustedConfigMessage(
      {
        type: CONFIG_MESSAGE_TYPE,
        protocolVersion: 1,
        config: { allowedOrigins: [], extra: true },
      },
      config,
    ),
  );
});

test("trusted messages can only narrow the manifest policy", () => {
  const config = loadManifestConfig(
    extensionManifest(["https://api.example.com", "https://cdn.example.com"]),
  );
  const narrowed = narrowConfig(config, {
    allowedOrigins: ["https://api.example.com"],
    allowedOperations: ["status"],
    maxPageMessageBytes: 1_024,
    maxPageStringLength: 512,
  });

  assert.deepEqual(narrowed.allowedOrigins, ["https://api.example.com"]);
  assert.deepEqual(narrowed.allowedOperations, ["status"]);
  assert.equal(narrowed.maxPageMessageBytes, 1_024);
  assert.equal(narrowed.maxPageStringLength, 512);
  assert.equal(Object.isFrozen(narrowed), true);

  assertCode("not_allowed", () =>
    narrowConfig(config, { allowedOrigins: ["https://evil.example"] }),
  );
  assertCode("not_allowed", () =>
    narrowConfig(config, { allowedOperations: ["relay", "admin"] }),
  );
  assertCode("invalid_value", () =>
    narrowConfig(config, { maxPageMessageBytes: 4_096 }),
  );
});

test("inherited and sparse arrays cannot pollute policy", () => {
  const config = loadManifestConfig(extensionManifest());
  const update = Object.create({
    allowedOrigins: ["https://evil.example"],
  }) as Record<string, unknown>;

  assert.deepEqual(narrowConfig(config, update).allowedOrigins, [
    "https://api.example.com",
  ]);

  const sparse = [] as unknown[];
  sparse.length = 1;
  assertCode("invalid_shape", () =>
    narrowConfig(config, { allowedOrigins: sparse }),
  );

  const prototype = Object.prototype as Record<string, unknown>;
  prototype.allowedOrigins = ["https://evil.example"];
  try {
    assert.deepEqual(narrowConfig(config, {}).allowedOrigins, [
      "https://api.example.com",
    ]);
  } finally {
    delete prototype.allowedOrigins;
  }
});

test("only an extension-page runtime sender is trusted", () => {
  const runtime = new FakeRuntime("extension-id", extensionManifest());
  assert.equal(
    isTrustedExtensionSender(
      {
        id: "extension-id",
        url: "chrome-extension://extension-id/background.js",
        origin: "chrome-extension://extension-id",
      },
      runtime.id,
    ),
    true,
  );
  assert.equal(
    isTrustedExtensionSender(
      {
        id: "extension-id",
        url: "https://attacker.example/page",
        origin: "https://attacker.example",
      },
      runtime.id,
    ),
    false,
  );

  const inherited = Object.create({ id: "extension-id" });
  assert.equal(isTrustedExtensionSender(inherited, runtime.id), false);
});
