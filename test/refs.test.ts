import { test } from "node:test";
import assert from "node:assert/strict";
import {
  LayeredConfigStore,
  RefCycleError,
  RefResolutionError,
} from "../src/index.ts";
import { capture } from "./helpers.ts";

test("cross-field reference resolves and links its provenance", () => {
  const s = new LayeredConfigStore();
  s.addLayer({
    id: "base",
    data: {
      palette: { primary: "#fff" },
      button: { color: { $ref: "#/palette/primary" } },
    },
  });

  const cfg = s.snapshot().config as { button: { color: string } };
  assert.equal(cfg.button.color, "#fff");

  const rec = s.explainAt("/button/color");
  assert.equal(rec?.resolvedFrom, "/palette/primary");
  assert.equal(rec?.value, "#fff");
  assert.equal(s.snapshot().dependencies["/button/color"], "/palette/primary");
});

test("references chain through other references", () => {
  const s = new LayeredConfigStore();
  s.addLayer({
    id: "base",
    data: {
      a: 42,
      b: { $ref: "#/a" },
      c: { $ref: "#/b" },
    },
  });
  const cfg = s.snapshot().config as { c: number };
  assert.equal(cfg.c, 42);
});

test("reference to a missing path fails with the ref's own path", () => {
  const s = new LayeredConfigStore();
  const err = capture(
    () => s.addLayer({ id: "bad", data: { x: { $ref: "#/nope" } } }),
    RefResolutionError,
  );
  assert.equal(err.pointer, "/x");
});

test("malformed references are rejected with their path", () => {
  const s = new LayeredConfigStore();
  const err = capture(
    () => s.addLayer({ id: "bad", data: { x: { $ref: "http://evil" } } }),
    RefResolutionError,
  );
  assert.equal(err.pointer, "/x");
});

test("reference cycles are detected and reported as a pointer chain", () => {
  const s = new LayeredConfigStore();
  const err = capture(
    () =>
      s.addLayer({
        id: "bad",
        data: { a: { $ref: "#/b" }, b: { $ref: "#/a" } },
      }),
    RefCycleError,
  );
  assert.deepEqual(err.cycle, ["/a", "/b", "/a"]);
});

test("self references are cycles too", () => {
  const s = new LayeredConfigStore();
  const err = capture(
    () => s.addLayer({ id: "bad", data: { a: { $ref: "#/a" } } }),
    RefCycleError,
  );
  assert.deepEqual(err.cycle, ["/a", "/a"]);
});

test("toggling a layer rewires reference dependencies without stale values", () => {
  const s = new LayeredConfigStore({ context: { env: "dev" } });
  s.addLayer({
    id: "base",
    data: { host: "dev-host", url: { $ref: "#/host" } },
  });
  s.addLayer({
    id: "prod",
    data: { host: "prod-host" },
    enabledWhen: (ctx) => ctx.env === "prod",
  });

  const urlOf = () => (s.snapshot().config as { url: string }).url;
  assert.equal(urlOf(), "dev-host");

  s.setContext({ env: "prod" });
  assert.equal(urlOf(), "prod-host", "enabled layer must re-target the ref");

  s.setContext({ env: "dev" });
  assert.equal(urlOf(), "dev-host", "disabling must not keep the stale ref value");
});

test("references owned by a disabled layer disappear from config and graph", () => {
  const s = new LayeredConfigStore({ context: { beta: true } });
  s.addLayer({ id: "base", data: { host: "h" } });
  s.addLayer({
    id: "beta",
    data: { alias: { $ref: "#/host" } },
    enabledWhen: (ctx) => ctx.beta === true,
  });

  assert.equal((s.snapshot().config as { alias: string }).alias, "h");
  assert.equal(s.snapshot().dependencies["/alias"], "/host");

  s.setContext({ beta: false });
  const cfg = s.snapshot().config as Record<string, unknown>;
  assert.equal(Object.hasOwn(cfg, "alias"), false);
  assert.equal(s.snapshot().dependencies["/alias"], undefined);
  assert.equal(s.explainAt("/alias"), undefined);
});

test("a failed compile leaves the previous configuration fully usable", () => {
  const s = new LayeredConfigStore();
  s.addLayer({ id: "base", data: { a: 1 } });
  const before = s.snapshot();

  capture(
    () => s.addLayer({ id: "bad", data: { x: { $ref: "#/missing" } } }),
    RefResolutionError,
  );

  assert.equal(s.snapshot(), before);
  // And the store still accepts good commits afterwards.
  s.addLayer({ id: "ok", data: { b: 2 } });
  assert.deepEqual(s.snapshot().config, { a: 1, b: 2 });
});
