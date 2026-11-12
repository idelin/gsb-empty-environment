import assert from "node:assert/strict";
import test from "node:test";

import {
  ExportsResolutionError,
  resolveExport,
  resolveExportsResult,
  resolveExports,
  resolveModuleSpecifier,
  tryResolveExports,
} from "./index.ts";

function rejectsWithCode(
  action: () => unknown,
  code: string,
): void {
  assert.throws(
    action,
    (error: unknown) =>
      error instanceof ExportsResolutionError && error.code === code,
  );
}

test("resolves the root export and direct package targets", () => {
  assert.equal(
    resolveExports("pkg", { ".": "./dist/index.js" }, "."),
    "./dist/index.js",
  );
  assert.equal(
    resolveExports("pkg", "./dist/index.js", "."),
    "./dist/index.js",
  );
});

test("matches conditional targets in declaration order", () => {
  const exportsMap = {
    ".": {
      node: "./dist/node.js",
      import: "./dist/import.js",
      require: "./dist/require.cjs",
      default: "./dist/default.js",
    },
  };

  assert.equal(resolveExports("pkg", exportsMap, "."), "./dist/node.js");
});

test("recurses through nested condition objects and reaches default", () => {
  const exportsMap = {
    ".": {
      require: "./dist/require.cjs",
      node: {
        require: "./dist/node-require.cjs",
        default: "./dist/node.js",
      },
      default: "./dist/default.js",
    },
  };

  assert.equal(resolveExports("pkg", exportsMap, "."), "./dist/node.js");
});

test("uses the longest matching wildcard prefix", () => {
  const exportsMap = {
    "./feature/*": "./dist/feature/*.js",
    "./feature/deep/*": "./dist/feature/deep/*.js",
    "./feature/deep/very/*": "./dist/feature/very/*.js",
  };

  assert.equal(
    resolveExports("pkg", exportsMap, "./feature/deep/very/one"),
    "./dist/feature/very/one.js",
  );
  assert.equal(
    resolveExports("pkg", exportsMap, "./feature/deep/two"),
    "./dist/feature/deep/two.js",
  );
  assert.equal(
    resolveExports("pkg", exportsMap, "./feature/three"),
    "./dist/feature/three.js",
  );
});

test("an exact key wins before wildcard patterns", () => {
  const exportsMap = {
    "./feature/*": "./dist/*.js",
    "./feature/special": "./dist/special.cjs",
  };

  assert.equal(
    resolveExports("pkg", exportsMap, "./feature/special"),
    "./dist/special.cjs",
  );
});

test("rejects traversal and encoded separators in wildcard replacements", () => {
  const exportsMap = {
    "./feature/*": "./dist/*.js",
  };

  for (const subpath of [
    "./feature/../secret",
    "./feature/%2e%2e/secret",
    "./feature/%252e%252e/secret",
    "./feature/one%2Ftwo",
    "./feature/one%2ftwo",
    "./feature/one%5Ctwo",
    "./feature/one%5ctwo",
    "./feature/node_modules/evil",
    "./feature/NODE_MODULES/evil",
  ]) {
    rejectsWithCode(
      () => resolveExports("pkg", exportsMap, subpath),
      "ERR_INVALID_PACKAGE_SUBPATH",
    );
  }
});

test("rejects empty wildcard replacements", () => {
  const exportsMap = {
    "./feature/*": "./dist/*.js",
  };

  rejectsWithCode(
    () => resolveExports("pkg", exportsMap, "./feature/"),
    "ERR_INVALID_PACKAGE_SUBPATH",
  );
});

test("rejects targets outside the package or through node_modules", () => {
  const exportsMap = {
    "./bad/relative": "../outside.js",
    "./bad/dotdot": "./dist/../outside.js",
    "./bad/encoded": "./dist/%2e%2e/outside.js",
    "./bad/absolute": "/outside.js",
    "./bad/node-modules": "./node_modules/evil/index.js",
  };

  for (const subpath of Object.keys(exportsMap)) {
    rejectsWithCode(
      () => resolveExports("pkg", exportsMap, subpath),
      "ERR_INVALID_PACKAGE_TARGET",
    );
  }
});

test("null targets block exact and wildcard exports", () => {
  const exportsMap = {
    "./internal": null,
    "./feature/*": null,
  };

  rejectsWithCode(
    () => resolveExports("pkg", exportsMap, "./internal"),
    "ERR_PACKAGE_PATH_NOT_EXPORTED",
  );
  rejectsWithCode(
    () => resolveExports("pkg", exportsMap, "./feature/one"),
    "ERR_PACKAGE_PATH_NOT_EXPORTED",
  );
});

test("does not fall back to file-system-style lookup", () => {
  const exportsMap = {
    "./public/*": "./dist/public/*.js",
  };

  rejectsWithCode(
    () => resolveExports("pkg", exportsMap, "./private/secret.js"),
    "ERR_PACKAGE_PATH_NOT_EXPORTED",
  );
  assert.equal(
    tryResolveExports("pkg", exportsMap, "./private/secret.js"),
    null,
  );
});

test("returns an object result and a canonical module specifier", () => {
  const exportsMap = {
    "./feature/*": "./dist/feature/*.js",
  };

  assert.equal(
    resolveModuleSpecifier("pkg", exportsMap, "./feature/one"),
    "pkg/dist/feature/one.js",
  );
  assert.deepEqual(
    resolveExportsResult("pkg", exportsMap, "./feature/one"),
    {
      packageName: "pkg",
      subpath: "./feature/one",
      target: "./dist/feature/one.js",
      specifier: "pkg/dist/feature/one.js",
    },
  );
  assert.equal(
    resolveExport("pkg", exportsMap, "./feature/one"),
    "./dist/feature/one.js",
  );
});
