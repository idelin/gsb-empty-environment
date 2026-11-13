import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { test } from "node:test";

import {
  checkIntegrity,
  IntegrityError,
  loadModule,
  resolveImportMap,
  resolveImportMapDetailed,
  verifyIntegrity,
} from "./index.ts";

const base = "https://example.com/";
const body = "export const value = 42;\n";

function sri(algorithm: "sha256" | "sha384" | "sha512", content: string): string {
  const digest = createHash(algorithm).update(content).digest("base64");
  return `${algorithm}-${digest}`;
}

const correctIntegrity = sri("sha384", body);
const wrongIntegrity = `sha384-${createHash("sha384")
  .update("different content")
  .digest("base64")}`;

test("scopes use the longest matching prefix regardless of insertion order", () => {
  const shortPrefix = `${base}lib/`;
  const longPrefix = `${base}lib/deep/`;
  const importMap = {
    scopes: {
      // The short prefix deliberately appears first. It must not win.
      [shortPrefix]: { pkg: "/short.js" },
      [longPrefix]: { pkg: "/long.js" },
    },
  };

  const shortResolution = resolveImportMapDetailed(importMap, "pkg", {
    baseUrl: base,
    moduleUrl: `${base}lib/entry.js`,
  });
  const longResolution = resolveImportMapDetailed(importMap, "pkg", {
    baseUrl: base,
    moduleUrl: `${base}lib/deep/entry.js`,
  });

  assert.equal(shortResolution?.url, `${base}short.js`);
  assert.equal(shortResolution?.scope, shortPrefix);
  assert.equal(longResolution?.url, `${base}long.js`);
  assert.equal(longResolution?.scope, longPrefix);
});

test("scope length wins even when the longer prefix is declared first", () => {
  const importMap = {
    scopes: {
      [`${base}lib/deep/`]: { pkg: "/long-first.js" },
      [`${base}lib/`]: { pkg: "/short-second.js" },
    },
  };

  assert.equal(
    resolveImportMap(importMap, "pkg", {
      baseUrl: base,
      moduleUrl: `${base}lib/deep/entry.js`,
    }),
    `${base}long-first.js`,
  );
});

test("the winning longer scope does not fall through to a shorter scope", () => {
  const importMap = {
    imports: { pkg: "/global.js" },
    scopes: {
      [`${base}lib/`]: { pkg: "/short-scope.js" },
      [`${base}lib/deep/`]: { other: "/long-scope-only.js" },
    },
  };

  assert.equal(
    resolveImportMap(importMap, "pkg", {
      baseUrl: base,
      moduleUrl: `${base}lib/deep/entry.js`,
    }),
    `${base}global.js`,
  );
});

test("exact imports match succeeds when there are no scopes", () => {
  const importMap = { imports: { pkg: "/pkg.js" } };
  const resolution = resolveImportMapDetailed(importMap, "pkg", {
    baseUrl: base,
    moduleUrl: `${base}app/main.js`,
  });

  assert.equal(resolution?.url, `${base}pkg.js`);
  assert.equal(resolution?.scope, null);
  assert.equal(resolution?.mapKey, "pkg");
  assert.equal(resolution?.matchType, "imports-exact");
});

test("URL-like specifiers resolve directly without a mapping", () => {
  const importMap = { imports: { pkg: "/pkg.js" } };
  assert.equal(
    resolveImportMap(importMap, "./local.js", { baseUrl: `${base}app/` }),
    `${base}app/local.js`,
  );
  assert.equal(
    resolveImportMap(importMap, "https://cdn.example/mod.js", {
      baseUrl: base,
    }),
    "https://cdn.example/mod.js",
  );
});

test("the selected scope falls back to imports only when its key is absent", () => {
  const importMap = {
    imports: { other: "/fallback.js" },
    scopes: {
      [`${base}lib/`]: { pkg: "/scoped.js" },
    },
  };

  assert.equal(
    resolveImportMap(importMap, "pkg", {
      baseUrl: base,
      moduleUrl: `${base}lib/entry.js`,
    }),
    `${base}scoped.js`,
  );
  assert.equal(
    resolveImportMap(importMap, "other", {
      baseUrl: base,
      moduleUrl: `${base}lib/entry.js`,
    }),
    `${base}fallback.js`,
  );
});

test("a matching scoped null entry blocks instead of falling back", () => {
  const importMap = {
    imports: { pkg: "/unscoped.js" },
    scopes: {
      [`${base}lib/`]: { pkg: null },
    },
  };

  const resolution = resolveImportMapDetailed(importMap, "pkg", {
    baseUrl: base,
    moduleUrl: `${base}lib/entry.js`,
  });
  assert.equal(resolution?.blocked, true);
  assert.equal(resolution?.url, null);
});

test("prefix entries append the unmatched suffix", () => {
  const importMap = { imports: { "lib/": "/vendor/" } };
  assert.equal(
    resolveImportMap(importMap, "lib/tools/a.js", { baseUrl: base }),
    `${base}vendor/tools/a.js`,
  );
});

test("loadModule accepts matching integrity metadata", async () => {
  const importMap = {
    imports: {
      pkg: { url: "/pkg.js", integrity: correctIntegrity },
    },
  };
  const fetches: string[] = [];

  const loaded = await loadModule(importMap, "pkg", {
    baseUrl: base,
    fetch: (url) => {
      fetches.push(url);
      return body;
    },
  });

  assert.deepEqual(fetches, [`${base}pkg.js`]);
  assert.equal(loaded.url, `${base}pkg.js`);
  assert.equal(new TextDecoder().decode(loaded.content), body);
  assert.equal(checkIntegrity(body, correctIntegrity), true);
  assert.equal(checkIntegrity(body, wrongIntegrity), false);
});

test("a bad digest fails and never retries the integrity-free fallback URL", async () => {
  const importMap = {
    imports: {
      pkg: {
        url: "/pkg.js",
        integrity: wrongIntegrity,
        fallback: "/pkg-without-integrity.js",
      },
    },
  };
  const fetches: string[] = [];

  await assert.rejects(
    loadModule(importMap, "pkg", {
      baseUrl: base,
      fetch: (url) => {
        fetches.push(url);
        return body;
      },
    }),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === "INTEGRITY_MISMATCH" &&
      error.specifier === "pkg",
  );

  assert.deepEqual(fetches, [`${base}pkg.js`]);
});

test("integrity declared by URL in the import map is enforced", async () => {
  const importMap = {
    imports: { pkg: "/pkg.js" },
    integrity: {
      [`${base}pkg.js`]: correctIntegrity,
    },
  };

  const loaded = await loadModule(importMap, "pkg", {
    baseUrl: base,
    fetch: () => body,
  });
  assert.equal(loaded.url, `${base}pkg.js`);

  await assert.rejects(
    loadModule(importMap, "pkg", {
      baseUrl: base,
      fetch: () => "tampered",
    }),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === "INTEGRITY_MISMATCH",
  );
});

test("missing required integrity fails before fetching", async () => {
  const importMap = {
    imports: { pkg: { url: "/pkg.js" } },
  };
  let fetches = 0;

  await assert.rejects(
    loadModule(importMap, "pkg", {
      baseUrl: base,
      fetch: () => {
        fetches += 1;
        return body;
      },
    }),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === "INTEGRITY_MISSING",
  );

  assert.equal(fetches, 0);
});

test("missing metadata also fails when integrity is explicitly required", async () => {
  const importMap = { imports: { pkg: "/pkg.js" } };

  await assert.rejects(
    loadModule(importMap, "pkg", {
      baseUrl: base,
      requireIntegrity: true,
      fetch: () => body,
    }),
    (error: unknown) =>
      error instanceof IntegrityError && error.code === "INTEGRITY_MISSING",
  );

  await assert.rejects(verifyIntegrity(body, undefined), (error: unknown) => {
    return error instanceof IntegrityError && error.code === "INTEGRITY_MISSING";
  });
});

test("an unknown integrity algorithm fails and does not fall back", async () => {
  const importMap = {
    imports: {
      pkg: {
        url: "/pkg.js",
        integrity: "md5-AAAAAAAAAAAAAAAAAAAAAA==",
      },
    },
  };
  const fetches: string[] = [];

  await assert.rejects(
    loadModule(importMap, "pkg", {
      baseUrl: base,
      fetch: (url) => {
        fetches.push(url);
        return body;
      },
    }),
    (error: unknown) =>
      error instanceof IntegrityError &&
      error.code === "INTEGRITY_ALGORITHM_UNKNOWN" &&
      error.algorithm === "md5",
  );

  assert.deepEqual(fetches, [`${base}pkg.js`]);
});
