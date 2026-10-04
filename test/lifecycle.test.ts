import { test } from "node:test";
import assert from "node:assert/strict";
import {
  DependencyCycleError,
  ModuleAlreadyDefinedError,
  ModuleLifecycle,
  ModuleLoadError,
  ModuleNotDefinedError,
} from "../src/lifecycle";

function makeKernel() {
  const tornDown: string[] = [];
  const kernel = new ModuleLifecycle();
  kernel
    .define({
      name: "config",
      setup: () => ({ conn: "config" }),
      teardown: () => void tornDown.push("config"),
    })
    .define({
      name: "db",
      dependencies: ["config"],
      setup: () => ({ conn: "db" }),
      teardown: () => void tornDown.push("db"),
    })
    .define({
      name: "service",
      dependencies: ["db", "config"],
      setup: () => ({ conn: "service" }),
      teardown: () => void tornDown.push("service"),
    });
  return { kernel, tornDown };
}

test("反复装载同名模块：每次返回独立句柄，引用计数递增", () => {
  const { kernel } = makeKernel();
  const h1 = kernel.load("db");
  const h2 = kernel.load("db");
  const h3 = kernel.load("db");
  assert.equal(kernel.refCount("db"), 3);
  assert.equal(h1.generation, h2.generation);
  assert.equal(h1.instance, h3.instance);

  h1.release();
  assert.equal(kernel.refCount("db"), 2);
  assert.ok(kernel.isLoaded("db"));
  h2.release();
  h3.release();
  assert.equal(kernel.isLoaded("db"), false);
});

test("旧释放函数晚调用不卸载新模块；重复释放无副作用", () => {
  const { kernel } = makeKernel();
  const oldHandle = kernel.load("db");
  oldHandle.release();
  assert.equal(kernel.isLoaded("db"), false);

  // 重新装载得到新世代
  const newHandle = kernel.load("db");
  assert.equal(newHandle.generation, oldHandle.generation + 1);
  assert.equal(kernel.refCount("db"), 1);

  // 旧句柄晚调用：不得影响新世代
  oldHandle.release();
  assert.ok(kernel.isLoaded("db"));
  assert.equal(kernel.refCount("db"), 1);

  // 重复释放：无副作用
  newHandle.release();
  newHandle.release();
  newHandle.release();
  assert.equal(kernel.isLoaded("db"), false);
  assert.equal(kernel.refCount("db"), 0);
});

test("依赖引用登记与逆序清理", () => {
  const { kernel, tornDown } = makeKernel();
  const handle = kernel.load("service");
  // service -> db -> config，且 service 直接依赖 config
  assert.equal(kernel.refCount("config"), 2);
  assert.equal(kernel.refCount("db"), 1);
  assert.deepEqual(kernel.loadedModules(), ["config", "db", "service"]);

  handle.release();
  // 卸载顺序：service 先 teardown，然后逆序释放其依赖（config 后于 db 登记，先释放，
  // 但 config 仍被 db 引用，计数归零发生在 db 卸载时）
  assert.deepEqual(tornDown, ["service", "db", "config"]);
  assert.equal(kernel.loadedModules().length, 0);
});

test("装载失败回滚已登记依赖", () => {
  const kernel = new ModuleLifecycle();
  const tornDown: string[] = [];
  kernel
    .define({ name: "a", setup: () => "a", teardown: () => void tornDown.push("a") })
    .define({ name: "b", setup: () => "b", teardown: () => void tornDown.push("b") })
    .define({
      name: "broken",
      dependencies: ["a", "b"],
      setup: () => {
        throw new Error("boom");
      },
    });

  assert.throws(() => kernel.load("broken"), (err: unknown) => {
    assert.ok(err instanceof ModuleLoadError);
    assert.match((err as Error).message, /broken/);
    return true;
  });
  // a、b 的引用被回滚并卸载
  assert.deepEqual(tornDown, ["b", "a"]);
  assert.equal(kernel.isLoaded("a"), false);
  assert.equal(kernel.isLoaded("b"), false);
  // 失败后可正常重试
  kernel.define({ name: "broken2", dependencies: ["a"], setup: () => "ok" });
  assert.ok(kernel.load("broken2").instance);
});

test("依赖循环被检测并报告路径", () => {
  const kernel = new ModuleLifecycle();
  kernel
    .define({ name: "x", dependencies: ["y"] })
    .define({ name: "y", dependencies: ["z"] })
    .define({ name: "z", dependencies: ["x"] });

  assert.throws(() => kernel.load("x"), (err: unknown) => {
    assert.ok(err instanceof DependencyCycleError);
    assert.deepEqual((err as DependencyCycleError).path, ["x", "y", "z", "x"]);
    assert.match((err as Error).message, /x -> y -> z -> x/);
    return true;
  });
  // 循环失败后注册表保持干净
  assert.equal(kernel.loadedModules().length, 0);
});

test("teardown 抛错不中断依赖清理", () => {
  const kernel = new ModuleLifecycle();
  const tornDown: string[] = [];
  kernel
    .define({ name: "leaf", teardown: () => void tornDown.push("leaf") })
    .define({
      name: "parent",
      dependencies: ["leaf"],
      teardown: () => {
        throw new Error("teardown boom");
      },
    });
  const handle = kernel.load("parent");
  handle.release();
  assert.deepEqual(tornDown, ["leaf"]);
  assert.equal(kernel.loadedModules().length, 0);
  assert.ok(kernel.events.some((e) => e.type === "unload:failure"));
});

test("shutdown 逆序卸载，之后旧句柄释放无副作用", () => {
  const { kernel, tornDown } = makeKernel();
  const h1 = kernel.load("service");
  const h2 = kernel.load("db");
  kernel.shutdown();
  assert.deepEqual(tornDown, ["service", "db", "config"]);
  assert.equal(kernel.loadedModules().length, 0);

  h1.release();
  h2.release();
  assert.deepEqual(tornDown, ["service", "db", "config"]);
});

test("未定义模块与重复定义报错", () => {
  const { kernel } = makeKernel();
  assert.throws(() => kernel.load("nope"), ModuleNotDefinedError);
  assert.throws(
    () => kernel.define({ name: "db" }),
    ModuleAlreadyDefinedError,
  );
});

test("生命周期日志可解释且覆盖关键事件", () => {
  const { kernel } = makeKernel();
  const handle = kernel.load("service");
  handle.release();
  const log = kernel.formatLog();
  assert.match(log, /\[load:start\] service/);
  assert.match(log, /\[load:success\] service/);
  assert.match(log, /登记对依赖 "db" 的引用/);
  assert.match(log, /\[unload:start\] service/);
  assert.match(log, /\[unload:success\] config/);
  // 序号单调递增
  const seqs = kernel.events.map((e) => e.seq);
  assert.deepEqual(seqs, [...seqs].sort((a, b) => a - b));
});
