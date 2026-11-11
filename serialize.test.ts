import test from "node:test";
import assert from "node:assert/strict";
import { tag } from "./tagged.ts";
import {
  CycleError,
  ForbiddenTagError,
  serializeJson,
  writeJson,
} from "./serialize.ts";

function recorder() {
  const chunks: string[] = [];
  return {
    chunks,
    write(chunk: string) {
      chunks.push(chunk);
    },
  };
}

function throwsAs<E extends Error>(
  fn: () => void,
  ctor: new (...args: never[]) => E,
): E {
  try {
    fn();
  } catch (e) {
    assert.ok(e instanceof ctor, `expected ${ctor.name}, got ${e}`);
    return e as E;
  }
  assert.fail("expected function to throw");
}

test("无禁止标签时正常序列化并写出", () => {
  const doc = tag(
    { user: tag("alice", "pii"), n: [1, tag(2, "n")], ok: true },
    "meta",
  );
  const expected = JSON.stringify({ user: "alice", n: [1, 2], ok: true });
  assert.equal(serializeJson(doc, { forbid: ["secret"] }), expected);

  const sink = recorder();
  writeJson(doc, sink, { forbid: ["secret"] });
  assert.deepEqual(sink.chunks, [expected]);

  const fnChunks: string[] = [];
  writeJson(doc, (c: string) => fnChunks.push(c), { forbid: [] });
  assert.deepEqual(fnChunks, [expected]);
});

test("按路径拒绝带禁止标签的嵌套值，接收器未收到前缀", () => {
  const doc = {
    ok: 1,
    creds: { password: tag("hunter2", "secret") },
  };
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(doc, sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(
    err.violations.map((v) => v.path),
    ["$.creds.password"],
  );
  assert.deepEqual(err.violations[0].tags, ["secret"]);
  assert.deepEqual(sink.chunks, []);
});

test("别名：同一带标值出现在多条路径时全部报告且不写出", () => {
  const secret = tag("x", "secret");
  const doc = { a: secret, b: { c: secret } };
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(doc, sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(
    err.violations.map((v) => v.path).sort(),
    ["$.a", "$.b.c"],
  );
  assert.deepEqual(sink.chunks, []);
});

test("循环：含禁止标签的循环结构被拒绝且不写出", () => {
  const node: { self?: unknown; leak?: unknown } = {};
  node.self = node;
  node.leak = tag("x", "secret");
  const sink = recorder();
  assert.throws(
    () => writeJson(node, sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(sink.chunks, []);
});

test("循环：纯循环结构报 CycleError 且不写出", () => {
  const node: { self?: unknown } = {};
  node.self = node;
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(node, sink, { forbid: ["secret"] }),
    CycleError,
  );
  assert.equal(err.path, "$.self");
  assert.deepEqual(sink.chunks, []);
});

test("getter 异常在写出前抛出，接收器未收到前缀", () => {
  const doc = {
    safe: tag("v", "secret"),
    get boom(): unknown {
      throw new Error("getter blew up");
    },
  };
  const sink = recorder();
  assert.throws(() => writeJson(doc, sink, { forbid: ["secret"] }), /blew up/);
  assert.deepEqual(sink.chunks, []);
});

test("getter 返回的带标值同样被检查", () => {
  const doc = {
    get lazy(): unknown {
      return tag("late", "secret");
    },
  };
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(doc, sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(
    err.violations.map((v) => v.path),
    ["$.lazy"],
  );
  assert.deepEqual(sink.chunks, []);
});

test("共享子树：从多个父级可达的禁止值被拒绝且不写出", () => {
  const shared = { token: tag("t", "secret") };
  const doc = { x: shared, y: [shared, { z: shared }] };
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(doc, sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(
    err.violations.map((v) => v.path),
    ["$.x.token"],
  );
  assert.deepEqual(sink.chunks, []);
});

test("共享子树：无禁止标签时正常序列化", () => {
  const shared = { token: tag("t", "pii") };
  const doc = { x: shared, y: [shared, { z: shared }] };
  const sink = recorder();
  writeJson(doc, sink, { forbid: ["secret"] });
  assert.deepEqual(sink.chunks, [
    JSON.stringify({ x: { token: "t" }, y: [{ token: "t" }, { z: { token: "t" } }] }),
  ]);
});

test("根值携带禁止标签时路径为 $", () => {
  const sink = recorder();
  const err = throwsAs(
    () => writeJson(tag("root", "secret"), sink, { forbid: ["secret"] }),
    ForbiddenTagError,
  );
  assert.deepEqual(
    err.violations.map((v) => v.path),
    ["$"],
  );
  assert.deepEqual(sink.chunks, []);
});
