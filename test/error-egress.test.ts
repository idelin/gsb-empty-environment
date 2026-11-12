import assert from "node:assert/strict";
import test from "node:test";

import {
  createErrorEgressInterceptor,
  redactMessage,
  sanitizeError,
} from "../src/error-egress.ts";
import type { AnyDetail } from "../src/error-egress.ts";

class FakeConnectError extends Error {
  code: string;
  details: AnyDetail[];
  metadata: Headers;
  constructor(code: string, message: string, details: AnyDetail[] = []) {
    super(message);
    this.name = "ConnectError";
    this.code = code;
    this.details = details;
    this.metadata = new Headers();
  }
}

interface LogEntry {
  message: string;
  meta?: Record<string, unknown>;
}

function makeLogger(): { logs: LogEntry[]; logger: { error: (m: string, meta?: Record<string, unknown>) => void } } {
  const logs: LogEntry[] = [];
  return {
    logs,
    logger: {
      error(message: string, meta?: Record<string, unknown>) {
        logs.push({ message, meta });
      },
    },
  };
}

function detail(typeUrl: string, size = 8): AnyDetail {
  return { typeUrl, value: new Uint8Array(size) };
}

// ---- 内部错误：message 替换 + 关联 ID + 原始信息只进日志 --------------------

test("internal 错误：message 被替换为固定文案与关联 ID，原文进日志", async () => {
  const { logs, logger } = makeLogger();
  const interceptor = createErrorEgressInterceptor({
    logger,
    idGenerator: () => "ref-1234",
  });
  const secret = "dial tcp 10.20.30.40:5432 db=postgres password=hunter2";
  const next = async () => {
    throw new FakeConnectError("internal", `connect failed: ${secret}`, [
      detail("google.rpc.DebugInfo"),
    ]);
  };

  await assert.rejects(
    interceptor(next)({}),
    (err: unknown) => {
      const e = err as FakeConnectError;
      assert.equal(e.code, "internal");
      assert.equal(e.message, "Internal server error (ref: ref-1234)");
      assert.ok(!e.message.includes("10.20.30.40"));
      assert.ok(!e.message.includes("hunter2"));
      assert.deepEqual(e.details, []);
      return true;
    },
  );

  assert.equal(logs.length, 1);
  assert.match(logs[0].message, /ref-1234/);
  assert.ok(String(logs[0].meta?.message).includes(secret));
});

test("unknown 错误（含数字枚举 code=2）同样被隐藏", () => {
  const { logs, logger } = makeLogger();
  const err = new FakeConnectError("unknown", "panic: runtime error at /srv/app/main.go:42");
  (err as { code: unknown }).code = 2;
  const out = sanitizeError(err, { logger, idGenerator: () => "ref-x" }) as FakeConnectError;
  assert.equal(out.message, "Internal server error (ref: ref-x)");
  assert.deepEqual(out.details, []);
  assert.equal(logs.length, 1);
});

// ---- details 白名单 ---------------------------------------------------------

test("非白名单的 details 整条丢弃，白名单内保留", () => {
  const { logger } = makeLogger();
  const err = new FakeConnectError("invalid_argument", "bad field", [
    detail("acme.v1.FieldViolation"),
    detail("google.rpc.DebugInfo"),
    detail("acme.v1.InternalTrace"),
  ]);
  const out = sanitizeError(err, {
    logger,
    allowedDetailTypes: ["acme.v1.FieldViolation"],
  }) as FakeConnectError;
  assert.deepEqual(
    out.details.map((d) => d.typeUrl),
    ["acme.v1.FieldViolation"],
  );
});

// ---- details 超长 -----------------------------------------------------------

test("details 编码总长超过 4096 字节时全部丢弃", () => {
  const { logger } = makeLogger();
  const err = new FakeConnectError("invalid_argument", "bad field", [
    detail("acme.v1.FieldViolation", 3000),
    detail("acme.v1.FieldViolation", 3000),
  ]);
  const out = sanitizeError(err, {
    logger,
    allowedDetailTypes: ["acme.v1.FieldViolation"],
  }) as FakeConnectError;
  assert.deepEqual(out.details, []);

  const small = new FakeConnectError("invalid_argument", "bad field", [
    detail("acme.v1.FieldViolation", 100),
  ]);
  const kept = sanitizeError(small, {
    logger,
    allowedDetailTypes: ["acme.v1.FieldViolation"],
  }) as FakeConnectError;
  assert.equal(kept.details.length, 1);
});

// ---- 元数据泄漏 -------------------------------------------------------------

test("响应 header 与 trailer 中的敏感键被删除", async () => {
  const interceptor = createErrorEgressInterceptor({});
  const header = new Headers({
    "content-type": "application/proto",
    authorization: "Bearer secret-token",
    "x-internal-trace-id": "abc123",
    "X-Internal-Host": "db-01.internal",
  });
  const trailer = new Headers({
    "grpc-status": "0",
    cookie: "session=secret",
    "set-cookie": "sid=abc; HttpOnly",
  });
  const next = async () => ({ header, trailer, message: { ok: true } });

  const res = (await interceptor(next)({})) as {
    header: Headers;
    trailer: Headers;
  };
  assert.equal(res.header.get("content-type"), "application/proto");
  assert.equal(res.header.get("authorization"), null);
  assert.equal(res.header.get("x-internal-trace-id"), null);
  assert.equal(res.header.get("x-internal-host"), null);
  assert.equal(res.trailer.get("grpc-status"), "0");
  assert.equal(res.trailer.get("cookie"), null);
  assert.equal(res.trailer.get("set-cookie"), null);
});

test("错误对象携带的 metadata 同样被清洗", () => {
  const { logger } = makeLogger();
  const err = new FakeConnectError("not_found", "user not found");
  err.metadata = new Headers({
    authorization: "Bearer leaked",
    "x-internal-node": "k8s-worker-3",
    "retry-after": "5",
  });
  const out = sanitizeError(err, { logger }) as FakeConnectError;
  assert.equal(out.metadata.get("authorization"), null);
  assert.equal(out.metadata.get("x-internal-node"), null);
  assert.equal(out.metadata.get("retry-after"), "5");
});

// ---- 可展示 code 的 message 打码 ---------------------------------------------

test("invalid_argument 保留 message，但 IPv4 / user:pass@ / 绝对路径被打码", () => {
  const { logger } = makeLogger();
  const err = new FakeConnectError(
    "invalid_argument",
    "cannot reach 192.168.1.10 from /etc/secrets/db.conf using admin:s3cret@db",
  );
  const out = sanitizeError(err, { logger }) as FakeConnectError;
  assert.equal(out.code, "invalid_argument");
  assert.ok(!out.message.includes("192.168.1.10"));
  assert.ok(!out.message.includes("admin:s3cret@"));
  assert.ok(!out.message.includes("/etc/secrets/db.conf"));
  assert.ok(out.message.includes("[redacted-ip]"));
  assert.ok(out.message.includes("***:***@"));
  assert.ok(out.message.includes("[redacted-path]"));
});

test("redactMessage 不打码普通文本与 URL 主机部分", () => {
  assert.equal(redactMessage("name must not be empty"), "name must not be empty");
  const redacted = redactMessage("see https://example.com/docs for details");
  assert.ok(redacted.includes("example.com"));
});
