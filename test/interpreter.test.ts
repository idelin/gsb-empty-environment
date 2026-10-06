import test from "node:test";
import assert from "node:assert/strict";
import {
  Interpreter,
  CancelledError,
  CleanupError,
  type Instr,
  type StepResult,
} from "../src/interpreter.ts";

const y = (value: unknown): Instr => ({ kind: "yield", value });
const ret = (value?: unknown): Instr => ({ kind: "return", value });
const effect = (fn: () => void): Instr => ({ kind: "do", effect: fn });
const tryF = (body: Instr[], fin: Instr[]): Instr => ({
  kind: "try",
  body,
  finally: fin,
});

function collect(it: Interpreter, log: string[]): StepResult[] {
  const out: StepResult[] = [];
  for (let i = 0; i < 100 && !it.done; i++) out.push(it.step());
  return out;
}

function capture(fn: () => void): unknown {
  try {
    fn();
  } catch (err) {
    return err;
  }
  throw new Error("expected function to throw");
}

test("正常返回：yield 序列后返回 return 值", () => {
  const it = new Interpreter([y(1), y(2), ret("done")]);
  assert.deepEqual(it.step(), { type: "yield", value: 1 });
  assert.deepEqual(it.step(), { type: "yield", value: 2 });
  assert.deepEqual(it.step(), { type: "return", value: "done" });
  assert.equal(it.done, true);
  assert.throws(() => it.step(), /already finished/);
});

test("取消在恢复点注入：挂起时 cancel，下一步抛出 CancelledError", () => {
  const it = new Interpreter([y("a"), y("b")]);
  assert.deepEqual(it.step(), { type: "yield", value: "a" });
  it.cancel();
  assert.equal(it.cancelPending, true);
  assert.throws(() => it.step(), (e: unknown) => e instanceof CancelledError);
  assert.equal(it.done, true);
});

test("取消只在恢复点注入：启动前 cancel 不影响首次 step", () => {
  const it = new Interpreter([y(1), y(2)]);
  it.cancel();
  assert.deepEqual(it.step(), { type: "yield", value: 1 }); // 启动不是恢复点
  assert.throws(() => it.step(), CancelledError); // 恢复点注入
});

test("嵌套清理：内层 finally 先跑，外层 finally 后跑，取消异常到达顶层", () => {
  const log: string[] = [];
  const it = new Interpreter([
    tryF(
      [tryF([y("inner")], [effect(() => log.push("inner-finally"))])],
      [effect(() => log.push("outer-finally"))],
    ),
  ]);
  assert.deepEqual(it.step(), { type: "yield", value: "inner" });
  it.cancel();
  assert.throws(() => it.step(), CancelledError);
  assert.deepEqual(log, ["inner-finally", "outer-finally"]);
});

test("finally 可继续 yield，且只执行一次", () => {
  let runs = 0;
  const it = new Interpreter([
    tryF([y("body")], [effect(() => runs++), y("cleanup-1"), y("cleanup-2")]),
  ]);
  assert.deepEqual(it.step(), { type: "yield", value: "body" });
  it.cancel();
  // 取消注入后进入 finally，finally 内的 yield 正常交付
  assert.deepEqual(it.step(), { type: "yield", value: "cleanup-1" });
  assert.deepEqual(it.step(), { type: "yield", value: "cleanup-2" });
  // finally 结束后原取消异常继续传播
  assert.throws(() => it.step(), CancelledError);
  assert.equal(runs, 1);
});

test("finally 在正常路径上也只执行一次", () => {
  let runs = 0;
  const it = new Interpreter([
    tryF([y(1)], [effect(() => runs++)]),
    ret("ok"),
  ]);
  const out = collect(it, []);
  assert.deepEqual(out, [
    { type: "yield", value: 1 },
    { type: "return", value: "ok" },
  ]);
  assert.equal(runs, 1);
});

test("再次取消：finally 内恢复点注入新取消，覆盖原取消并以 cause 关联", () => {
  const log: string[] = [];
  const it = new Interpreter([
    tryF(
      [y("body")],
      [y("cleanup"), effect(() => log.push("unreachable"))],
    ),
  ]);
  assert.deepEqual(it.step(), { type: "yield", value: "body" });
  it.cancel();
  assert.deepEqual(it.step(), { type: "yield", value: "cleanup" }); // 进入 finally
  it.cancel(); // 再次取消
  const err = capture(() => it.step());
  assert.ok(err instanceof CancelledError);
  assert.ok((err as Error).cause instanceof CancelledError); // 原取消保留为 cause
  assert.deepEqual(log, []); // finally 剩余部分被中止
});

test("清理异常覆盖原异常，原异常作为 cause 保留", () => {
  const original = new Error("body failed");
  const cleanup = new Error("cleanup failed");
  const it = new Interpreter([
    tryF(
      [effect(() => { throw original; })],
      [effect(() => { throw cleanup; })],
    ),
  ]);
  const err = capture(() => it.step());
  assert.equal(err, cleanup); // 清理异常覆盖
  assert.equal((err as Error).cause, original); // 原异常保留为关联信息
});

test("清理抛出非 Error 值时包装为 CleanupError 并关联原异常", () => {
  const original = new Error("body failed");
  const it = new Interpreter([
    tryF(
      [effect(() => { throw original; })],
      [effect(() => { throw "raw-cleanup"; })],
    ),
  ]);
  const err = capture(() => it.step());
  assert.ok(err instanceof CleanupError);
  assert.equal((err as CleanupError).cleanup, "raw-cleanup");
  assert.equal((err as Error).cause, original);
});

test("return 穿过 try 时 finally 仍执行一次", () => {
  const log: string[] = [];
  const it = new Interpreter([
    tryF([y(1), ret("early")], [effect(() => log.push("finally"))]),
    effect(() => log.push("unreachable")),
  ]);
  assert.deepEqual(it.step(), { type: "yield", value: 1 });
  assert.deepEqual(it.step(), { type: "return", value: "early" });
  assert.deepEqual(log, ["finally"]);
});

test("嵌套 finally 中再次取消：外层 finally 仍执行，新取消带 cause 到顶层", () => {
  const log: string[] = [];
  const it = new Interpreter([
    tryF(
      [tryF([y("deep")], [y("inner-cleanup")])],
      [effect(() => log.push("outer-finally"))],
    ),
  ]);
  assert.deepEqual(it.step(), { type: "yield", value: "deep" });
  it.cancel();
  assert.deepEqual(it.step(), { type: "yield", value: "inner-cleanup" });
  it.cancel(); // 内层 finally 中再次取消
  const err = capture(() => it.step());
  assert.ok(err instanceof CancelledError);
  assert.ok((err as Error).cause instanceof CancelledError);
  assert.deepEqual(log, ["outer-finally"]); // 外层清理仍然执行
});
