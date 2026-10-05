import { test } from "node:test";
import assert from "node:assert/strict";
import { Planner } from "../src/planner.ts";
import { PluginLoader } from "../src/loader.ts";
import { makePlugin, makeRegistry } from "./helpers.ts";

test("生命周期：初始化失败后，依赖它的插件标记失败，可独立工作的插件保留", () => {
  const registry = makeRegistry(
    makePlugin("base"),
    makePlugin("mid", { requires: { base: "*" } }),
    makePlugin("broken", {
      init: () => {
        throw new Error("boom");
      },
    }),
    makePlugin("app", { requires: { broken: "*" } }),
    makePlugin("opt", { optional: { broken: "*" } }),
    makePlugin("solo"),
  );
  const plan = new Planner(registry).plan([
    { name: "base" },
    { name: "mid" },
    { name: "broken" },
    { name: "app" },
    { name: "opt" },
    { name: "solo" },
  ]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();

  assert.equal(loader.getState("broken"), "failed");
  assert.equal(loader.getState("app"), "failed", "硬依赖失败插件的 app 应失败");
  assert.equal(loader.initCounts.get("app") ?? 0, 0, "app 的 init 不应被调用");
  assert.equal(loader.getState("opt"), "active", "可选依赖失败不应拖垮宿主");
  assert.equal(loader.getState("base"), "active");
  assert.equal(loader.getState("mid"), "active");
  assert.equal(loader.getState("solo"), "active");
  assert.equal(loader.initCounts.get("broken"), 1, "broken 只尝试初始化一次");
});

test("生命周期：卸载时只卸载依赖它的已加载部分", () => {
  const disposed: string[] = [];
  const track = (name: string) => () => {
    disposed.push(name);
  };
  const registry = makeRegistry(
    makePlugin("core", { dispose: track("core") }),
    makePlugin("lib", { requires: { core: "*" }, dispose: track("lib") }),
    makePlugin("app", { requires: { lib: "*" }, dispose: track("app") }),
    makePlugin("opt", { optional: { core: "*" }, dispose: track("opt") }),
    makePlugin("solo", { dispose: track("solo") }),
  );
  const plan = new Planner(registry).plan([
    { name: "app" },
    { name: "opt" },
    { name: "solo" },
  ]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();
  assert.equal(loader.getState("app"), "active");

  loader.unload("core");

  assert.deepEqual(disposed, ["app", "lib", "core"], "按依赖反向顺序卸载");
  assert.equal(loader.getState("app"), "unloaded");
  assert.equal(loader.getState("lib"), "unloaded");
  assert.equal(loader.getState("core"), "unloaded");
  assert.equal(loader.getState("opt"), "active", "可选依赖者保留");
  assert.equal(loader.getState("solo"), "active", "无关节插件保留");
  assert.equal(loader.disposeCounts.get("solo") ?? 0, 0);
});

test("动态请求：初始化回调请求插件不会重复初始化", () => {
  const seen: unknown[] = [];
  const registry = makeRegistry(
    makePlugin("alpha", {
      init: (ctx) => {
        seen.push(ctx.require("beta"));
        return { name: "alpha" };
      },
    }),
    makePlugin("beta", { init: () => ({ name: "beta-instance" }) }),
    makePlugin("gamma", {
      init: (ctx) => {
        seen.push(ctx.require("beta"));
        return { name: "gamma" };
      },
    }),
  );
  const plan = new Planner(registry).plan([{ name: "alpha" }, { name: "beta" }, { name: "gamma" }]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();

  assert.equal(loader.initCounts.get("beta"), 1, "beta 只初始化一次");
  assert.equal(loader.initCounts.get("alpha"), 1);
  assert.equal(loader.initCounts.get("gamma"), 1);
  assert.equal(loader.getState("beta"), "active");
  assert.ok(seen.length === 2 && seen[0] === seen[1], "两次动态请求拿到同一实例");
  assert.equal(seen[0], loader.getInstance("beta"));
});

test("动态请求：运行时可从注册表扩展计划并一次性初始化", () => {
  const registry = makeRegistry(
    makePlugin("alpha", {
      init: (ctx) => {
        ctx.require("extra");
        return { name: "alpha" };
      },
    }),
    makePlugin("base2"),
    makePlugin("extra", { requires: { base2: "*" } }),
  );
  const plan = new Planner(registry).plan([{ name: "alpha" }]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();

  assert.equal(loader.getState("extra"), "active");
  assert.equal(loader.getState("base2"), "active");
  assert.equal(loader.initCounts.get("extra"), 1);
  assert.equal(loader.initCounts.get("base2"), 1);
});

test("动态请求：循环请求被检测并且双方失败", () => {
  const registry = makeRegistry(
    makePlugin("a", {
      init: (ctx) => {
        ctx.require("b");
        return "a";
      },
    }),
    makePlugin("b", {
      init: (ctx) => {
        ctx.require("a");
        return "b";
      },
    }),
  );
  const plan = new Planner(registry).plan([{ name: "a" }, { name: "b" }]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();

  assert.equal(loader.getState("a"), "failed");
  assert.equal(loader.getState("b"), "failed");
  assert.match(String(loader.getFailure("b")), /循环/);
  assert.equal(loader.initCounts.get("a"), 1);
  assert.equal(loader.initCounts.get("b"), 1);
});

test("生命周期：可选依赖缺席时 ctx.get 返回 undefined，插件照常工作", () => {
  let ghostValue: unknown = "unset";
  const registry = makeRegistry(
    makePlugin("ui", {
      optional: { ghost: "*" },
      init: (ctx) => {
        ghostValue = ctx.get("ghost");
        return { name: "ui" };
      },
    }),
  );
  const plan = new Planner(registry).plan([{ name: "ui" }]);
  const loader = new PluginLoader(plan, registry);
  loader.initializeAll();

  assert.equal(loader.getState("ui"), "active");
  assert.equal(ghostValue, undefined);
});
