import assert from "node:assert/strict";
import test from "node:test";

import {
  BoundaryError,
  MessageBoundaryMiddleware,
  createMessageBoundaryMiddleware,
  type BoundaryErrorCode,
} from "../index.ts";

function parse(raw: string) {
  return createMessageBoundaryMiddleware().parseMessage(Buffer.from(raw, "latin1"));
}

function expectCode(raw: string, code: BoundaryErrorCode): void {
  assert.throws(
    () => parse(raw),
    (error: unknown) =>
      error instanceof BoundaryError && error.code === code,
    `expected ${code}`,
  );
}

test("accepts a legal Content-Length request", () => {
  const result = parse(
    "POST /submit HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 4\r\n" +
      "\r\n" +
      "Wiki",
  );

  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;
  assert.equal(result.value.body.toString(), "Wiki");
  assert.deepEqual(result.value.headers, [
    ["host", "example.test"],
    ["content-length", "4"],
  ]);
  assert.equal(result.value.boundary.kind, "content-length");
  assert.equal(result.value.boundary.forwarding.mode, "content-length");
  assert.equal(result.value.boundary.forwarding.contentLength, 4);
});

test("accepts a legal chunked request and canonicalizes it to Content-Length", () => {
  const result = parse(
    "POST /submit HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "4\r\n" +
      "Wiki\r\n" +
      "0\r\n" +
      "\r\n",
  );

  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;
  assert.equal(result.value.body.toString(), "Wiki");
  assert.deepEqual(result.value.headers, [
    ["host", "example.test"],
    ["content-length", "4"],
  ]);
  assert.equal(result.value.boundary.kind, "chunked");
  assert.equal(result.value.boundary.wireBodyLength, 14);
  assert.equal(result.value.boundary.forwarding.mode, "content-length");
  assert.equal(result.value.boundary.forwarding.contentLength, 4);
});

test("rejects CL.TE", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 6\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\n" +
      "\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
    "BOTH_CONTENT_LENGTH_AND_TRANSFER_ENCODING",
  );
});

test("rejects TE.CL", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "Content-Length: 6\r\n" +
      "\r\n" +
      "0\r\n" +
      "\r\n" +
      "GET /admin HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "\r\n",
    "BOTH_CONTENT_LENGTH_AND_TRANSFER_ENCODING",
  );
});

test("rejects duplicate Transfer-Encoding header fields", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n",
    "DUPLICATE_TRANSFER_ENCODING",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "Transfer-Encoding: gzip\r\n" +
      "\r\n" +
      "0\r\n\r\n",
    "DUPLICATE_TRANSFER_ENCODING",
  );
});

test("rejects duplicate transfer-coding tokens", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked, chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n",
    "DUPLICATE_TRANSFER_ENCODING",
  );
});

test("rejects a final transfer-coding that is not chunked", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: gzip\r\n" +
      "\r\n",
    "TRANSFER_ENCODING_NOT_CHUNKED",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked, gzip\r\n" +
      "\r\n",
    "TRANSFER_ENCODING_NOT_CHUNKED",
  );
});

test("rejects extra transfer-codings instead of guessing their semantics", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: gzip, chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n",
    "UNSUPPORTED_TRANSFER_CODING",
  );
});

test("rejects duplicate or list-form Content-Length", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 4\r\n" +
      "Content-Length: 4\r\n" +
      "\r\n" +
      "Wiki",
    "DUPLICATE_CONTENT_LENGTH",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 4, 4\r\n" +
      "\r\n" +
      "Wiki",
    "INVALID_CONTENT_LENGTH",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: +4\r\n" +
      "\r\n" +
      "Wiki",
    "INVALID_CONTENT_LENGTH",
  );
});

test("rejects header-name whitespace and obsolete folding", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding : chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n",
    "INVALID_HEADER",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "X-Test: one\r\n" +
      " two\r\n" +
      "\r\n",
    "OBSOLETE_LINE_FOLDING",
  );
});

test("accepts canonical chunk extensions", () => {
  const result = parse(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "4;foo=bar;flag\r\n" +
      "Wiki\r\n" +
      "0;done=\"a b\"\r\n" +
      "\r\n",
  );

  assert.equal(result.status, "complete");
  if (result.status === "complete") {
    assert.equal(result.value.body.toString(), "Wiki");
  }
});

test("rejects ambiguous chunk extensions", () => {
  const cases = [
    "4; foo=bar\r\nWiki\r\n0\r\n\r\n",
    "4;foo=bar baz\r\nWiki\r\n0\r\n\r\n",
    "4;foo=1;foo=2\r\nWiki\r\n0\r\n\r\n",
    "4;foo=\r\nWiki\r\n0\r\n\r\n",
    "4;foo=\"unterminated\r\nWiki\r\n0\r\n\r\n",
    "4;foo=\"bar\" baz\r\nWiki\r\n0\r\n\r\n",
  ];

  for (const chunk of cases) {
    assert.throws(
      () =>
        parse(
          "POST / HTTP/1.1\r\n" +
            "Host: example.test\r\n" +
            "Transfer-Encoding: chunked\r\n" +
            "\r\n" +
            chunk,
        ),
      (error: unknown) =>
        error instanceof BoundaryError &&
        (error.code === "AMBIGUOUS_CHUNK_EXTENSION" ||
          error.code === "INVALID_CHUNK_EXTENSION"),
    );
  }
});

test("accepts the canonical final-chunk terminator", () => {
  const result = parse(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\n\r\n",
  );
  assert.equal(result.status, "complete");
  if (result.status === "complete") {
    assert.equal(result.value.body.length, 0);
    assert.equal(result.value.boundary.wireBodyLength, 5);
  }
});

test("rejects chunk terminator variants", () => {
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0 \r\n\r\n",
    "INVALID_CHUNK_SIZE",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\r\n",
    "INVALID_CHUNK_TERMINATOR",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\n\n",
    "INVALID_CHUNK_TERMINATOR",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "0\r\nX-Trailer: value\r\n\r\n",
    "TRAILERS_NOT_ALLOWED",
  );
  expectCode(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Transfer-Encoding: chunked\r\n" +
      "\r\n" +
      "4\r\nWikiXX\r\n0\r\n\r\n",
    "INVALID_CHUNK_TERMINATOR",
  );
});

test("rejects bare LF in the request head", () => {
  expectCode(
    "POST / HTTP/1.1\nHost: example.test\r\nContent-Length: 0\r\n\r\n",
    "INVALID_HEADER",
  );
});

test("h2c upgrade is rejected unless downgrade is explicit", () => {
  const request =
    "GET / HTTP/1.1\r\n" +
    "Host: example.test\r\n" +
    "Connection: Upgrade, HTTP2-Settings\r\n" +
    "Upgrade: h2c\r\n" +
    "HTTP2-Settings: AAMAAABkAAQAAP__\r\n" +
    "\r\n";

  expectCode(request, "UPGRADE_NOT_ALLOWED");

  const middleware = new MessageBoundaryMiddleware({
    allowH2cDowngrade: true,
  });
  const result = middleware.parseMessage(Buffer.from(request, "latin1"));
  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;

  assert.equal(result.value.boundary.kind, "none");
  assert.equal(result.value.boundary.upgrade?.h2cDowngraded, true);
  assert.deepEqual(result.value.headers, [["host", "example.test"]]);
  for (const [name] of result.value.headers) {
    assert.notEqual(name, "upgrade");
    assert.notEqual(name, "connection");
    assert.notEqual(name, "http2-settings");
  }
});

test("h2c requires valid settings and explicit policy", () => {
  expectCode(
    "GET / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Connection: Upgrade\r\n" +
      "Upgrade: h2c\r\n" +
      "\r\n",
    "H2C_SETTINGS_MISSING",
  );

  expectCode(
    "GET / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Connection: Upgrade, HTTP2-Settings\r\n" +
      "Upgrade: h2c\r\n" +
      "HTTP2-Settings: not base64!\r\n" +
      "\r\n",
    "INVALID_H2C_SETTINGS",
  );
});

test("generic HTTP/1.1 Upgrade is rejected by default", () => {
  expectCode(
    "GET /chat HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Connection: Upgrade\r\n" +
      "Upgrade: websocket\r\n" +
      "\r\n",
    "UPGRADE_NOT_ALLOWED",
  );
});

test("generic Upgrade with body framing is rejected even when enabled", () => {
  const middleware = new MessageBoundaryMiddleware({
    allowGenericUpgrade: true,
  });
  assert.throws(
    () =>
      middleware.parseMessage(
        Buffer.from(
          "POST /chat HTTP/1.1\r\n" +
            "Host: example.test\r\n" +
            "Connection: Upgrade\r\n" +
            "Upgrade: websocket\r\n" +
            "Content-Length: 4\r\n" +
            "\r\n" +
            "Wiki",
          "latin1",
        ),
      ),
    (error: unknown) =>
      error instanceof BoundaryError && error.code === "UPGRADE_WITH_BODY",
  );
});

test("generic Upgrade is forwarded once when explicitly enabled", () => {
  const middleware = new MessageBoundaryMiddleware({
    allowGenericUpgrade: true,
  });
  const result = middleware.parseMessage(
    Buffer.from(
      "GET /chat HTTP/1.1\r\n" +
        "Host: example.test\r\n" +
        "Connection: keep-alive, Upgrade\r\n" +
        "Upgrade: websocket\r\n" +
        "\r\n",
      "latin1",
    ),
  );

  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;
  assert.equal(result.value.boundary.kind, "upgrade");
  assert.ok(result.value.boundary.headEnd > 0);
  assert.equal(
    result.value.boundary.bodyStart,
    result.value.boundary.headEnd,
  );
  const connectionHeaders = result.value.headers.filter(
    ([name]) => name === "connection",
  );
  const upgradeHeaders = result.value.headers.filter(
    ([name]) => name === "upgrade",
  );
  assert.deepEqual(connectionHeaders, [["connection", "upgrade"]]);
  assert.deepEqual(upgradeHeaders, [["upgrade", "websocket"]]);
});

test("Content-Length body is never scanned for a second request", () => {
  const smuggled = "GET /admin HTTP/1.1\r\nHost: example.test\r\n\r\n";
  const body = `xxxx${smuggled}`;
  const result = parse(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      `Content-Length: ${Buffer.byteLength(body)}\r\n` +
      "\r\n" +
      body,
  );

  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;
  assert.equal(result.value.body.toString(), body);
  assert.equal(result.value.nextOffset, result.value.boundary.bodyEnd);
});

test("offsets are absolute when parsing after a prefix", () => {
  const request = Buffer.from(
    "POST / HTTP/1.1\r\n" +
      "Host: example.test\r\n" +
      "Content-Length: 4\r\n" +
      "\r\n" +
      "Wiki",
    "latin1",
  );
  const prefix = Buffer.from("skip", "latin1");
  const buffer = Buffer.concat([prefix, request]);

  const result = createMessageBoundaryMiddleware().parseMessage(
    buffer,
    prefix.length,
  );
  assert.equal(result.status, "complete");
  if (result.status !== "complete") return;
  assert.equal(result.value.body.toString(), "Wiki");
  assert.equal(result.value.nextOffset, buffer.length);
  assert.equal(result.value.boundary.headEnd, prefix.length + request.length - 4);
  assert.equal(result.value.boundary.bodyEnd, buffer.length);
});
