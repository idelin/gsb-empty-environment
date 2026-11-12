import assert from "node:assert/strict";
import test from "node:test";
import { snapshotError, snapshotToJson } from "../index.ts";

const SECRET = "sk-live-9f8e7d6c5b4a-token";

test("基本快照：name/message/stack 与嵌套 cause 链", () => {
  const cause = new Error("db connection failed");
  const err = new Error("request failed", { cause });
  const { snapshot, nodeCount, truncated } = snapshotError(err) as {
    snapshot: any;
    nodeCount: number;
    truncated: boolean;
  };
  assert.equal(truncated, false);
  assert.equal(nodeCount, 2);
  assert.equal(snapshot.$type, "Error");
  assert.equal(snapshot.props.message, "request failed");
  assert.equal(typeof snapshot.props.stack, "string");
  const causeNode = snapshot.props.cause;
  assert.equal(causeNode.$type, "Error");
  assert.equal(causeNode.props.message, "db connection failed");
  assert.notEqual(snapshot.$id, causeNode.$id);
});

test("恶意访问器与 toJSON 不会被执行，也不会泄漏", () => {
  let getterCalls = 0;
  let toJsonCalls = 0;
  const err: any = new Error("boom");
  Object.defineProperty(err, "token", {
    enumerable: true,
    get() {
      getterCalls++;
      return SECRET;
    },
  });
  Object.defineProperty(err, "cause", {
    enumerable: true,
    get() {
      getterCalls++;
      throw new Error("pwned");
    },
  });
  err.toJSON = () => {
    toJsonCalls++;
    return { leaked: SECRET };
  };

  const json = snapshotToJson(err);
  assert.equal(getterCalls, 0);
  assert.equal(toJsonCalls, 0);
  assert.ok(!json.includes(SECRET), "输出不得包含秘密");
  assert.ok(!json.includes("pwned"));
  assert.ok(json.includes("[omitted: accessor]"));
  assert.ok(json.includes("[function"), "toJSON 应作为函数标记输出而非被调用");
});

test("循环 cause 图：输出 $ref 指向首次定义的 $id", () => {
  const err: any = new Error("self-caused");
  err.cause = err;
  const { snapshot } = snapshotError(err) as { snapshot: any };
  assert.deepEqual(snapshot.props.cause, { $ref: snapshot.$id });
  // 序列化必须可行且不含循环结构
  const parsed = JSON.parse(snapshotToJson(err));
  assert.deepEqual(parsed.props.cause, { $ref: parsed.$id });
});

test("共享 cause：第二次出现使用相同引用编号", () => {
  const shared = new Error("shared root");
  const a: any = new Error("a");
  const b: any = new Error("b");
  a.cause = shared;
  b.cause = shared;
  const agg: any = new AggregateError([a, b], "batch failed");
  const { snapshot, nodeCount } = snapshotError(agg) as { snapshot: any; nodeCount: number };
  const [first, second] = snapshot.props.errors.items;
  const sharedId = first.props.cause.$id;
  assert.equal(typeof sharedId, "number");
  assert.deepEqual(second.props.cause, { $ref: sharedId });
  assert.equal(nodeCount, 5); // agg + errors 数组 + a + b + shared（shared 只计一次）
});

test("敏感字段按策略替换或删除，嵌套 cause 同样生效", () => {
  const cause: any = new Error("inner");
  cause.password = "hunter2";
  cause.traceId = "keep-me";
  const err: any = new Error("outer", { cause });
  err.token = SECRET;
  err.apiKey = "ak-123456";
  err.sessionId = "sess-should-go";
  err.note = "harmless";

  const json = snapshotToJson(err, {
    fieldPolicy: { sessionId: "remove" },
  });
  for (const leaked of [SECRET, "hunter2", "ak-123456", "sess-should-go"]) {
    assert.ok(!json.includes(leaked), `泄漏了 ${leaked}`);
  }
  assert.ok(json.includes("[REDACTED]"));
  assert.ok(json.includes("keep-me"));
  assert.ok(json.includes("harmless"));
  assert.ok(!json.includes("sessionId"), "remove 策略应连键名一起删除");
});

test("自定义 redactKeys 与自定义替换文本", () => {
  const err: any = new Error("x");
  err.tenantSecret = "tenant-key-001";
  const json = snapshotToJson(err, {
    redactKeys: ["tenantSecret"],
    replacement: "***",
  });
  assert.ok(!json.includes("tenant-key-001"));
  assert.ok(json.includes('"***"'));
});

test("节点预算：大图被截断且 nodeCount 有界", () => {
  const root: any = new Error("root");
  let cur = root;
  for (let i = 0; i < 500; i++) {
    const next: any = new Error(`level ${i}`);
    cur.cause = next;
    cur = next;
  }
  const { nodeCount, truncated } = snapshotError(root, { maxNodes: 25, maxDepth: 1000 });
  assert.equal(truncated, true);
  assert.ok(nodeCount <= 25);
  const json = snapshotToJson(root, { maxNodes: 25, maxDepth: 1000 });
  assert.ok(json.includes("[truncated: node budget exhausted]"));
});

test("文本预算：超长字符串被截断，输出总长度有界", () => {
  const err: any = new Error("x");
  err.payload = "A".repeat(100_000);
  const json = snapshotToJson(err, { maxStringLength: 64 });
  assert.ok(json.includes("…[truncated"));
  assert.ok(json.length < 1000, `输出过长: ${json.length}`);
});

test("属性预算：超宽对象被截断", () => {
  const err: any = new Error("wide");
  for (let i = 0; i < 200; i++) err[`k${i}`] = i;
  const { snapshot, truncated } = snapshotError(err, { maxProperties: 10 }) as {
    snapshot: any;
    truncated: boolean;
  };
  assert.equal(truncated, true);
  assert.ok(Object.keys(snapshot.props).length <= 10);
  assert.ok(snapshot.$truncated.includes("omitted"));
});

test("原对象不被修改", () => {
  const cause: any = new Error("inner");
  cause.token = SECRET;
  cause.meta = { attempts: 3, tags: ["a", "b"] };
  const err: any = new Error("outer", { cause });
  err.headers = { authorization: "Bearer abc", "x-trace": "t-1" };

  const before = JSON.stringify({
    token: cause.token,
    meta: cause.meta,
    headers: err.headers,
    message: err.message,
    causeIsSame: err.cause === cause,
  });
  snapshotToJson(err);
  const after = JSON.stringify({
    token: cause.token,
    meta: cause.meta,
    headers: err.headers,
    message: err.message,
    causeIsSame: err.cause === cause,
  });
  assert.equal(after, before);
  assert.equal(cause.token, SECRET);
  assert.equal(err.headers.authorization, "Bearer abc");
});

test("自有数据属性中的秘密会被脱敏（非枚举属性也处理）", () => {
  const err: any = new Error("x");
  Object.defineProperty(err, "secret", {
    value: "top-secret-value",
    enumerable: false,
    writable: true,
    configurable: true,
  });
  const json = snapshotToJson(err);
  assert.ok(!json.includes("top-secret-value"));
  assert.ok(json.includes("[REDACTED]"));
});

test("快照可安全 JSON.stringify：无 toJSON 劫持风险", () => {
  const err: any = new Error("x");
  err.nested = { toJSON: "not-a-function", value: 1 };
  const json = snapshotToJson(err);
  const parsed = JSON.parse(json);
  assert.equal(parsed.props.nested.props.value, 1);
});
