import { test } from "node:test";
import assert from "node:assert/strict";
import {
  guardResponse,
  guardNegotiated,
  guardMultipart,
  TypeGuardError,
} from "../src/index.ts";

function expectCode(code: string, fn: () => unknown): TypeGuardError {
  assert.throws(
    fn,
    (error: unknown) => error instanceof TypeGuardError && error.code === code,
    `expected TypeGuardError with code ${code}`,
  );
  try {
    fn();
  } catch (error) {
    return error as TypeGuardError;
  }
  throw new Error("unreachable");
}

test("bypass: query parameter cannot override declared type", () => {
  const error = expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { query: { type: "text/html" } },
    ),
  );
  assert.equal(error.detail.parameter, "type");
  assert.equal(error.detail.requested, "text/html");
});

test("bypass: response-content-type and format query params are blocked", () => {
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { query: { "response-content-type": "application/octet-stream" } },
    ),
  );
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { query: { format: "svg" } },
    ),
  );
});

test("bypass: query parameter cannot force inline for an executable type", () => {
  const error = expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "application/x-msdownload", filename: "setup.exe" },
      { query: { disposition: "inline" } },
    ),
  );
  assert.equal(error.detail.required, "attachment");
});

test("bypass: download flag cannot force attachment for an inline-safe type", () => {
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { query: { download: "1" } },
    ),
  );
});

test("bypass: request header cannot override declared type", () => {
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { headers: { "Content-Type": "text/html" } },
    ),
  );
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { headers: { "X-Response-Content-Type": "application/pdf" } },
    ),
  );
});

test("bypass: request header cannot dictate disposition", () => {
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "text/html", filename: "page.html" },
      { headers: { "Content-Disposition": "inline" } },
    ),
  );
  expectCode("negotiation-bypass", () =>
    guardResponse(
      { declaredType: "image/png", filename: "pic.png" },
      { headers: { "X-Response-Content-Disposition": "attachment" } },
    ),
  );
});

test("bypass: Accept header cannot change the declared type", () => {
  const headers = guardResponse(
    { declaredType: "image/png", filename: "pic.png" },
    { headers: { Accept: "text/html,application/xhtml+xml" } },
  );
  assert.equal(headers["content-type"], "image/png");
  assert.equal(headers["content-disposition"], "inline; filename=\"pic.png\"; filename*=UTF-8''pic.png");
});

test("bypass: negotiated representations with inconsistent declared types are rejected", () => {
  const error = expectCode("negotiation-bypass", () =>
    guardNegotiated([
      { declaredType: "application/json", filename: "data.json" },
      { declaredType: "text/html", filename: "data.html" },
    ]),
  );
  assert.deepEqual([...(error.detail.declaredTypes as string[])].sort(), [
    "application/json",
    "text/html",
  ]);
});

test("bypass: an empty representation set cannot be negotiated", () => {
  expectCode("negotiation-bypass", () => guardNegotiated([]));
});

test("bypass: an empty multipart response is rejected", () => {
  expectCode("negotiation-bypass", () => guardMultipart([]));
});

test("bypass: multipart parts of the same resource with differing types are rejected", () => {
  const error = expectCode("negotiation-bypass", () =>
    guardMultipart(
      [
        { name: "a", declaredType: "application/json", filename: "a.json" },
        { name: "b", declaredType: "text/html", filename: "b.html" },
      ],
      undefined,
      { sameResource: true },
    ),
  );
  assert.equal(error.code, "negotiation-bypass");
});

test("bypass: upstream content-type header conflicting with declared type is rejected", () => {
  expectCode("negotiation-bypass", () =>
    guardResponse({
      declaredType: "image/png",
      filename: "pic.png",
      headers: { "Content-Type": "text/html; charset=utf-8" },
    }),
  );
});

test("matching override values are ignored rather than changing the decision", () => {
  const headers = guardResponse(
    { declaredType: "image/png", filename: "pic.png" },
    {
      query: { type: "image/png", download: "0", inline: "1" },
      headers: { Accept: "*/*", "Content-Disposition": "inline" },
    },
  );
  assert.equal(headers["content-type"], "image/png");
  assert.match(headers["content-disposition"], /^inline/);
});

test("consistent representations negotiate successfully and emit Vary: Accept", () => {
  const headers = guardNegotiated([
    { declaredType: "application/json", filename: "data.json" },
    { declaredType: "application/json; charset=utf-8", filename: "data.json" },
  ]);
  assert.equal(headers["content-type"], "application/json");
  assert.equal(headers.vary, "Accept");
  assert.equal(headers["x-content-type-options"], "nosniff");
});

test("multipart with distinct resources guards every part independently", () => {
  const output = guardMultipart([
    { name: "doc", declaredType: "text/html", filename: "doc.html" },
    { name: "img", declaredType: "image/png", filename: "img.png" },
  ]);
  assert.equal(output.headers["x-content-type-options"], "nosniff");
  assert.match(output.headers["content-type"], /^multipart\/mixed; boundary=/);
  assert.match(output.parts[0]!.headers["content-disposition"], /^attachment/);
  assert.match(output.parts[1]!.headers["content-disposition"], /^inline/);
  assert.equal(output.boundary.length > 0, true);
});

test("multipart same-resource parts are accepted when types match", () => {
  const output = guardMultipart(
    [
      { name: "en", declaredType: "application/json", filename: "data.json" },
      { name: "fr", declaredType: "application/json", filename: "data.json" },
    ],
    undefined,
    { sameResource: true },
  );
  assert.equal(output.parts.length, 2);
  assert.equal(output.parts[0]!.declaredType, "application/json");
});
