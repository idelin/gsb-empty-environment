import { test } from "node:test";
import * as assert from "node:assert/strict";
import * as path from "path";
import { resolveChain } from "../src/resolve";
import { makeFixture } from "./helpers";

// Note: ancestors of the fixture root may themselves be symlinks
// (e.g. /var -> /private/var on macOS), so assertions about a specific
// link use the last hop, which is the one inside the fixture.

test("records symlink expansion chain for escaping link", () => {
  const fx = makeFixture();
  try {
    const p = path.join(fx.root, "declared", "escape-link");
    const r = resolveChain(p);
    assert.equal(r.exists, true);
    assert.equal(r.final, path.join(fx.realRoot, "outside", "secret.txt"));
    const last = r.chain[r.chain.length - 1];
    assert.equal(last.at, path.join(fx.realRoot, "declared", "escape-link"));
    assert.equal(last.target, path.join("..", "outside", "secret.txt"));
    assert.equal(last.resolved, r.final);
  } finally {
    fx.cleanup();
  }
});

test("resolves through a symlinked directory component", () => {
  const fx = makeFixture();
  try {
    const p = path.join(fx.root, "declared", "dir-link", "secret.txt");
    const r = resolveChain(p);
    assert.equal(r.exists, true);
    assert.equal(r.final, path.join(fx.realRoot, "outside", "secret.txt"));
    const last = r.chain[r.chain.length - 1];
    assert.equal(last.at, path.join(fx.realRoot, "declared", "dir-link"));
  } finally {
    fx.cleanup();
  }
});

test("reports first missing component for non-existent paths", () => {
  const fx = makeFixture();
  try {
    const p = path.join(fx.root, "declared", "nope", "file.txt");
    const r = resolveChain(p);
    assert.equal(r.exists, false);
    assert.equal(r.final, null);
    assert.equal(r.missingAt, path.join(fx.realRoot, "declared", "nope"));
  } finally {
    fx.cleanup();
  }
});

test("detects symlink loops", () => {
  const fx = makeFixture();
  try {
    const r = resolveChain(path.join(fx.root, "loop-a"));
    assert.equal(r.exists, false);
    assert.equal(r.error, "ELOOP");
    assert.ok(r.chain.length > 1);
  } finally {
    fx.cleanup();
  }
});
