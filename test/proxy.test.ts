import assert from "node:assert/strict";
import test from "node:test";

import {
  ProxyConnection,
  RecordingBackend,
  serializeForwardedRequest,
  verifyForwardedRequest,
  type BoundaryPolicy,
  type CloseInfo,
  type Rejection,
} from "../index.ts";

interface RunResult {
  readonly backend: RecordingBackend;
  readonly rejections: Rejection[];
  readonly closes: CloseInfo[];
  readonly connection: ProxyConnection;
}

async function run(raw: string, policy: BoundaryPolicy = {}): Promise<RunResult> {
  const backend = new RecordingBackend();
  const rejections: Rejection[] = [];
  const closes: CloseInfo[] = [];
  const connection = new ProxyConnection({
    backend: backend.handle,
    policy,
    onReject: (rejection) => {
      rejections.push(rejection);
    },
    onClose: (info) => {
      closes.push(info);
    },
  });

  await connection.feed(Buffer.from(raw, "latin1"));
  return { backend, rejections, closes, connection };
}

function assertSingleCanonicalMessage(result: RunResult, body: string): void {
  assert.equal(result.backend.requests.length, 1);
  const request = result.backend.requests[0];
  verifyForwardedRequest(request);
  assert.equal(request.body.toString(), body);

  const contentLength = request.headers.filter(
    ([name]) => name === "content-length",
  );
  const transferEncoding = request.headers.filter(
    ([name]) => name === "transfer-encoding",
  );
  assert.equal(contentLength.length, 1);
  assert.equal(contentLength[0][1], String(Buffer.byteLength(body)));
  assert.equal(transferEncoding.length, 0);
}

test("uses one boundary for a legal chunked request", async () => {
  const result = await run(
    "POST /upload HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "4\r\n" +
      "Wiki\r\n" +
      "0\r\n" +
      "\r\n",
  );

  assertSingleCanonicalMessage(result, "Wiki");
  const request = result.backend.requests[0];
  assert.equal(request.boundary.kind, "chunked");
  assert.equal(request.boundary.forwarding.mode, "content-length");
  assert.equal(request.boundary.forwarding.contentLength, 4);
  assert.equal(result.rejections.length, 0);
});

test("rejects CL.TE before the backend sees the body", async () => {
  const result = await run(
    "POST /victim HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 6\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
  );

  assert.equal(result.backend.requests.length, 0);
  assert.equal(result.rejections.length, 1);
  assert.equal(
    result.rejections[0].error.code,
    "BOTH_CONTENT_LENGTH_AND_TRANSFER_ENCODING",
  );
  assert.equal(result.connection.stateValue, "closed");
});

test("rejects TE.CL before the backend sees the body", async () => {
  const result = await run(
    "POST /victim HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "Content-Length: 6\r\n" +
      "\r\n" +
      "0\r\n\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
  );

  assert.equal(result.backend.requests.length, 0);
  assert.equal(result.rejections[0].error.code,
    "BOTH_CONTENT_LENGTH_AND_TRANSFER_ENCODING");
});

test("rejects duplicate TE fields before the backend", async () => {
  const result = await run(
    "POST /victim HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
  );

  assert.equal(result.backend.requests.length, 0);
  assert.equal(result.rejections[0].error.code, "DUPLICATE_TRANSFER_ENCODING");
});

test("clears residual bytes after a chunk terminator", async () => {
  const result = await run(
    "POST /upload HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "4\r\n" +
      "Wiki\r\n" +
      "0\r\n" +
      "\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
  );

  assertSingleCanonicalMessage(result, "Wiki");
  assert.equal(result.rejections.length, 1);
  assert.equal(result.rejections[0].error.code, "RESIDUAL_AFTER_MESSAGE");
  assert.ok(result.connection.residualBytes > 0);
  assert.equal(result.connection.bufferedBytes, 0);
  assert.equal(result.connection.stateValue, "closed");
});

test("keeps the connection reusable only after a clean boundary", async () => {
  const first =
    "POST /one HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Content-Length: 4\r\n" +
    "\r\n" +
    "Wiki";
  const second =
    "POST /two HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Transfer-Encoding: chunked\r\n" +
    "\r\n" +
    "2\r\n" +
    "ok\r\n" +
    "0\r\n\r\n";

  const backend = new RecordingBackend();
  const connection = new ProxyConnection({ backend: backend.handle });

  await connection.feed(Buffer.from(first, "latin1"));
  assert.equal(connection.bufferedBytes, 0);
  assert.equal(connection.stateValue, "idle");

  await connection.feed(Buffer.from(second, "latin1"));
  assert.equal(backend.requests.length, 2);
  assert.equal(backend.requests[0].requestNumber, 1);
  assert.equal(backend.requests[1].requestNumber, 2);
  assert.equal(backend.requests[1].body.toString(), "ok");
  verifyForwardedRequest(backend.requests[0]);
  verifyForwardedRequest(backend.requests[1]);
});

test("a fragmented chunk terminator ending in CR stays incomplete", async () => {
  const backend = new RecordingBackend();
  const connection = new ProxyConnection({ backend: backend.handle });
  const prefix =
    "POST /upload HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Transfer-Encoding: chunked\r\n" +
    "\r\n" +
    "4\r\n" +
    "Wiki\r\n" +
    "0\r";

  await connection.feed(Buffer.from(prefix, "latin1"));
  assert.equal(backend.requests.length, 0);
  assert.equal(connection.stateValue, "idle");

  await connection.feed(Buffer.from("\n\r\n", "latin1"));
  assertSingleCanonicalMessage(
    {
      backend,
      rejections: [],
      closes: [],
      connection,
    },
    "Wiki",
  );
});

test("body bytes that look like a request stay inside Content-Length", async () => {
  const smuggled = "GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n";
  const body = `xxxx${smuggled}`;
  const result = await run(
    "POST /victim HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      `Content-Length: ${Buffer.byteLength(body)}\r\n` +
      "\r\n" +
      body,
  );

  assertSingleCanonicalMessage(result, body);
  assert.equal(result.backend.requests.length, 1);
  assert.equal(result.rejections.length, 0);
  assert.equal(result.connection.stateValue, "idle");
});

test("default policy rejects pipelined bytes as residual", async () => {
  const pipelined =
    "GET /one HTTP/1.1\r\nHost: example.test\r\n\r\n" +
    "GET /two HTTP/1.1\r\nHost: example.test\r\n\r\n";
  const result = await run(pipelined);

  assert.equal(result.backend.requests.length, 1);
  assert.equal(result.backend.requests[0].target, "/one");
  assert.equal(result.rejections[0].error.code, "RESIDUAL_AFTER_MESSAGE");
});

test("pipelining must be explicitly enabled", async () => {
  const pipelined =
    "GET /one HTTP/1.1\r\nHost: example.test\r\n\r\n" +
    "GET /two HTTP/1.1\r\nHost: example.test\r\n\r\n";
  const result = await run(pipelined, { allowPipelining: true });

  assert.equal(result.backend.requests.length, 2);
  assert.equal(result.backend.requests[1].target, "/two");
  assert.equal(result.rejections.length, 0);
});

test("h2c is rejected by default", async () => {
  const result = await run(
    "GET / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Connection: Upgrade, HTTP2-Settings\r\n" +
      "Upgrade: h2c\r\n" +
      "HTTP2-Settings: AAMAAABkAAQAAP__\r\n" +
      "\r\n",
  );

  assert.equal(result.backend.requests.length, 0);
  assert.equal(result.rejections[0].error.code, "UPGRADE_NOT_ALLOWED");
});

test("h2c downgrade strips upgrade headers before forwarding", async () => {
  const result = await run(
    "GET /one HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Connection: Upgrade, HTTP2-Settings\r\n" +
      "Upgrade: h2c\r\n" +
      "HTTP2-Settings: AAMAAABkAAQAAP__\r\n" +
      "\r\n",
    { allowH2cDowngrade: true },
  );

  assert.equal(result.backend.requests.length, 1);
  const request = result.backend.requests[0];
  verifyForwardedRequest(request);
  assert.equal(request.boundary.upgrade?.h2cDowngraded, true);
  assert.equal(request.boundary.kind, "none");
  assert.deepEqual(request.headers, [["host", "example.test"]])

  await result.connection.feed(
    Buffer.from(
      "GET /two HTTP/1.1\r\nHost: example.test\r\n\r\n",
      "latin1",
    ),
  );
  assert.equal(result.backend.requests.length, 2);
  assert.equal(result.backend.requests[1].target, "/two");
});

test("timeout clears a partial body and cannot create an implicit request", async () => {
  const backend = new RecordingBackend();
  const closes: CloseInfo[] = [];
  const connection = new ProxyConnection({
    backend: backend.handle,
    onClose: (info) => {
      closes.push(info);
    },
  });

  const partial =
    "POST /victim HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Content-Length: 10\r\n" +
    "\r\n" +
    "abc";
  await connection.feed(Buffer.from(partial, "latin1"));
  assert.equal(backend.requests.length, 0);

  await connection.timeout();
  assert.equal(connection.stateValue, "closed");
  assert.equal(closes.at(-1)?.reason, "timeout");

  await connection.feed(
    Buffer.from(
      "GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n",
      "latin1",
    ),
  );
  assert.equal(backend.requests.length, 0);
});

test("half-close rejects an incomplete message instead of parsing a new one", async () => {
  const backend = new RecordingBackend();
  const rejections: Rejection[] = [];
  const connection = new ProxyConnection({
    backend: backend.handle,
    onReject: (rejection) => {
      rejections.push(rejection);
    },
  });

  const partial =
    "POST /victim HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Transfer-Encoding: chunked\r\n" +
    "\r\n" +
    "4\r\n" +
    "Wi";
  await connection.feed(Buffer.from(partial, "latin1"));
  assert.equal(backend.requests.length, 0);

  await connection.end();
  assert.equal(rejections.length, 1);
  assert.equal(rejections[0].error.code, "INCOMPLETE_MESSAGE");
  assert.equal(connection.stateValue, "closed");
  assert.equal(connection.bufferedBytes, 0);

  await connection.feed(
    Buffer.from(
      "GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n",
      "latin1",
    ),
  );
  assert.equal(backend.requests.length, 0);
});

test("half-close after a complete message is a normal close", async () => {
  const backend = new RecordingBackend();
  const rejections: Rejection[] = [];
  const connection = new ProxyConnection({
    backend: backend.handle,
    onReject: (rejection) => {
      rejections.push(rejection);
    },
  });

  await connection.feed(
    Buffer.from(
      "GET / HTTP/1.1\r\nHost: example.test\r\n\r\n",
      "latin1",
    ),
  );
  await connection.end();

  assert.equal(backend.requests.length, 1);
  assert.equal(rejections.length, 0);
  assert.equal(connection.stateValue, "closed");
});

test("serialized backend head has one canonical framing", async () => {
  const result = await run(
    "POST /upload HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "3;part=1\r\n" +
      "abc\r\n" +
      "2\r\n" +
      "de\r\n" +
      "0\r\n\r\n",
  );

  const request = result.backend.requests[0];
  const wire = serializeForwardedRequest(request);
  const headEnd = wire.indexOf("\r\n\r\n");
  assert.ok(headEnd > 0);
  const head = wire.subarray(0, headEnd).toString("latin1");
  const lines = head.split("\r\n").slice(1);
  const contentLengths = lines
    .filter((line) => line.toLowerCase().startsWith("content-length:"))
    .map((line) => Number(line.slice(line.indexOf(":") + 1)));
  const transferEncodings = lines.filter((line) =>
    line.toLowerCase().startsWith("transfer-encoding:")
  );

  assert.equal(contentLengths.length, 1);
  assert.equal(transferEncodings.length, 0);
  assert.equal(contentLengths[0], request.boundary.forwarding.contentLength);

  const backendBody = wire.subarray(headEnd + 4, headEnd + 4 + contentLengths[0]);
  assert.equal(backendBody.toString(), "abcde");
  assert.deepEqual(backendBody, request.body);
  assert.equal(
    backendBody.length,
    wire.length - (headEnd + 4),
    "backend must consume exactly the declared boundary",
  );
});
