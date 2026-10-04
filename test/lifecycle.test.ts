import test from "node:test";
import assert from "node:assert/strict";
import {
  DependencyCycleError,
  MemoryLogger,
  ModuleLoadError,
  ModuleLoader,
  ModuleNotDefinedError,
  SilentLogger,
} from "../src/index.ts";

function makeLoader() {
  const logger = new MemoryLogger();
  const loader = new ModuleLoader({ logger });
  return { loader, logger };
}

function capture<T extends Error>(fn: () => void, ctor: new (...args: never[]) => T): T {
  try {
    fn();
  } catch (error) {
    assert.ok(error instanceof ctor, `应抛出 ${ctor.name}，实际为 ${String(error)}`);
    return error as T;
  }
  assert.fail(`应抛出 ${ctor.name}，但未抛出任何错误`);
}

test("装载返回带实例的释放函数，释放后卸载并执行清理", () => {
  const { loader } = makeLoader();
  const disposed: string[] = [];
  loader.define("a", (ctx) => {
    ctx.onDispose(() => disposed.push("a"));
    return { name: "a" };
  });

  const release = loader.load<{ name: string }>("a");
  assert.equal(release.instance.name, "a");
  assert.equal(loader.isLoaded("a"), true);
  assert.equal(loader.refCount("a"), 1);

  release();
  assert.equal(loader.isLoaded("a"), false);
  assert.deepEqual(disposed, ["a"]);
});

test("同名模块反复装载共享实例，引用计数归零才卸载", () => {
  const { loader } = makeLoader();
  let created = 0;
  loader.define("a", () => ({ id: ++created }));

  const r1 = loader.load<{ id: number }>("a");
  const r2 = loader.load<{ id: number }>("a");
  const r3 = loader.load<{ id: number }>("a");
  assert.equal(created, 1);
  assert.equal(r1.instance, r2.instance);
  assert.equal(loader.refCount("a"), 3);

  r1();
  r2();
  assert.equal(loader.isLoaded("a"), true);
  r3();
  assert.equal(loader.isLoaded("a"), false);
});

test("旧释放函数晚调用不能卸载新模块", () => {
  const { loader, logger } = makeLoader();
  let created = 0;
  loader.define("a", () => ({ id: ++created }));

  const stale = loader.load("a");
  stale();
  assert.equal(loader.isLoaded("a"), false);

  const fresh = loader.load<{ id: number }>("a");
  assert.equal(fresh.instance.id, 2);
  assert.equal(fresh.generation, 2);

  stale();
  stale();
  assert.equal(loader.isLoaded("a"), true);
  assert.equal(loader.refCount("a"), 1);
  assert.equal(created, 2);

  const ignored = logger.ofType("release-ignored");
  assert.ok(ignored.length >= 1);
  assert.match(ignored[0]!.message, /重复释放|已卸载/);

  fresh();
  assert.equal(loader.isLoaded("a"), false);
});

test("重复释放无副作用：清理回调只执行一次", () => {
  const { loader } = makeLoader();
  let disposed = 0;
  loader.define("a", () => ({}), { dispose: () => void disposed++ });

  const release = loader.load("a");
  assert.equal(release.released, false);
  release();
  release();
  release();
  assert.equal(release.released, true);
  assert.equal(disposed, 1);
});

test("依赖通过 require 登记，卸载时按逆序清理", () => {
  const { loader } = makeLoader();
  const order: string[] = [];
  loader.define("a", () => ({}), { dispose: () => void order.push("a") });
  loader.define("b", (ctx) => {
    ctx.require("a");
    return {};
  }, { dispose: () => void order.push("b") });
  loader.define("c", (ctx) => {
    ctx.require("b");
    return {};
  }, { dispose: () => void order.push("c") });

  const release = loader.load("c");
  assert.deepEqual([loader.isLoaded("a"), loader.isLoaded("b"), loader.isLoaded("c")], [true, true, true]);

  release();
  assert.deepEqual(order, ["c", "b", "a"]);
  assert.deepEqual([loader.isLoaded("a"), loader.isLoaded("b"), loader.isLoaded("c")], [false, false, false]);
});

test("同级多个依赖按登记的逆序释放", () => {
  const { loader } = makeLoader();
  const order: string[] = [];
  for (const name of ["x", "y", "z"]) {
    loader.define(name, () => ({}), { dispose: () => void order.push(name) });
  }
  loader.define("root", (ctx) => {
    ctx.require("x");
    ctx.require("y");
    ctx.require("z");
    return {};
  });

  loader.load("root")();
  assert.deepEqual(order, ["z", "y", "x"]);
});

test("共享依赖在双方都释放后才卸载", () => {
  const { loader } = makeLoader();
  loader.define("shared", () => ({}));
  loader.define("left", (ctx) => (ctx.require("shared"), {}));
  loader.define("right", (ctx) => (ctx.require("shared"), {}));

  const left = loader.load("left");
  const right = loader.load("right");
  assert.equal(loader.refCount("shared"), 2);

  left();
  assert.equal(loader.isLoaded("shared"), true);
  right();
  assert.equal(loader.isLoaded("shared"), false);
});

test("装载失败：已登记依赖逆序回滚，错误可解释", () => {
  const { loader, logger } = makeLoader();
  const order: string[] = [];
  loader.define("ok1", () => ({}), { dispose: () => void order.push("ok1") });
  loader.define("ok2", () => ({}), { dispose: () => void order.push("ok2") });
  loader.define("bad", (ctx) => {
    ctx.require("ok1");
    ctx.require("ok2");
    throw new Error("磁盘不可用");
  });

  const error = capture(() => loader.load("bad"), ModuleLoadError);
  assert.match(error.message, /bad/);
  assert.match(error.message, /磁盘不可用/);
  assert.ok(error.cause instanceof Error);

  assert.deepEqual(order, ["ok2", "ok1"]);
  assert.deepEqual(
    [loader.isLoaded("bad"), loader.isLoaded("ok1"), loader.isLoaded("ok2")],
    [false, false, false],
  );

  const failure = logger.ofType("load-failure");
  assert.equal(failure.length, 1);
  assert.match(failure[0]!.message, /回滚/);
  assert.equal(logger.ofType("rollback").length, 1);

  const retry = loader.load("ok1");
  assert.equal(loader.isLoaded("ok1"), true);
  retry();
});

test("依赖循环：检测路径、整体回滚、装载器恢复可用", () => {
  const { loader, logger } = makeLoader();
  const disposed: string[] = [];
  loader.define("a", (ctx) => {
    ctx.require("b");
    return {};
  }, { dispose: () => void disposed.push("a") });
  loader.define("b", (ctx) => {
    ctx.require("c");
    return {};
  }, { dispose: () => void disposed.push("b") });
  loader.define("c", (ctx) => {
    ctx.require("a");
    return {};
  });

  const error = capture(() => loader.load("a"), DependencyCycleError);
  assert.deepEqual(error.cyclePath, ["a", "b", "c", "a"]);
  assert.match(error.message, /a -> b -> c -> a/);

  assert.deepEqual([loader.isLoaded("a"), loader.isLoaded("b"), loader.isLoaded("c")], [false, false, false]);
  assert.equal(logger.ofType("cycle-detected").length, 1);

  loader.define("d", () => 42);
  const release = loader.load<number>("d");
  assert.equal(release.instance, 42);
  release();
});

test("自依赖也被视为循环", () => {
  const { loader } = makeLoader();
  loader.define("self", (ctx) => {
    ctx.require("self");
    return {};
  });
  const error = capture(() => loader.load("self"), DependencyCycleError);
  assert.deepEqual(error.cyclePath, ["self", "self"]);
});

test("清理回调抛错不中断其余清理，记录 dispose-error 日志", () => {
  const { loader, logger } = makeLoader();
  const cleaned: string[] = [];
  loader.define("dep", () => ({}), { dispose: () => void cleaned.push("dep") });
  loader.define("main", (ctx) => {
    ctx.require("dep");
    ctx.onDispose(() => {
      throw new Error("清理失败");
    });
    ctx.onDispose(() => void cleaned.push("second"));
    return {};
  });

  loader.load("main")();
  assert.deepEqual(cleaned, ["second", "dep"]);
  assert.equal(logger.ofType("dispose-error").length, 1);
  assert.equal(loader.isLoaded("main"), false);
  assert.equal(loader.isLoaded("dep"), false);
});

test("装载未定义模块抛出 ModuleNotDefinedError", () => {
  const { loader } = makeLoader();
  assert.throws(() => loader.load("ghost"), ModuleNotDefinedError);
});

test("生命周期日志可解释：关键事件齐全且带中文说明", () => {
  const { loader, logger } = makeLoader();
  loader.define("base", () => ({}));
  loader.define("top", (ctx) => {
    ctx.require("base");
    return {};
  });
  loader.load("top")();

  const types = logger.events.map((event) => event.type);
  for (const expected of [
    "defined",
    "load-start",
    "dependency-registered",
    "load-success",
    "release",
    "unload-start",
    "dependency-release",
    "unloaded",
  ]) {
    assert.ok(types.includes(expected), `缺少日志事件 ${expected}`);
  }
  for (const event of logger.events) {
    assert.ok(event.message.length > 0);
    assert.ok(event.timestamp > 0);
  }
  assert.ok(logger.render().split("\n").length === logger.events.length);
});

test("默认日志器可替换为静默日志器", () => {
  const loader = new ModuleLoader({ logger: new SilentLogger() });
  loader.define("a", () => 1);
  loader.load("a")();
  assert.equal(loader.isLoaded("a"), false);
});
