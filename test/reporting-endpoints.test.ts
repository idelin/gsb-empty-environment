import { test } from "node:test";
import assert from "node:assert/strict";
import { parseReportingEndpoints } from "../src/reporting-endpoints.ts";

const BASE = "https://example.com/dir/page.html";

test("absolute and relative endpoint URLs resolve against the response URL", () => {
  const endpoints = parseReportingEndpoints(
    ['default="https://reports.example/collect", local="/reports", up="../r"'],
    BASE,
  );
  assert.equal(endpoints.get("default")?.href, "https://reports.example/collect");
  assert.equal(endpoints.get("local")?.href, "https://example.com/reports");
  assert.equal(endpoints.get("up")?.href, "https://example.com/r");
});

test("non-https endpoints are rejected", () => {
  const endpoints = parseReportingEndpoints(
    ['a="http://example.com/x", b="ftp://example.com/y", c="https://example.com/ok"'],
    BASE,
  );
  assert.equal(endpoints.has("a"), false);
  assert.equal(endpoints.has("b"), false);
  assert.equal(endpoints.get("c")?.href, "https://example.com/ok");
});

test("endpoints with userinfo are rejected", () => {
  const endpoints = parseReportingEndpoints(
    ['a="https://user:pass@example.com/x", b="https://user@example.com/y"'],
    BASE,
  );
  assert.equal(endpoints.size, 0);
});

test("non-string member values are skipped", () => {
  const endpoints = parseReportingEndpoints(
    ["a=https://token.example/x, b=123, c, d=:aGk=:, e=\"https://ok.example/\""],
    BASE,
  );
  assert.equal(endpoints.size, 1);
  assert.equal(endpoints.get("e")?.href, "https://ok.example/");
});

test("dictionary parse failure invalidates the entire header", () => {
  const endpoints = parseReportingEndpoints(
    ['good="https://a.example/", bad="unterminated'],
    BASE,
  );
  assert.equal(endpoints.size, 0);
});

test("conflicting duplicate keys across headers: first wins", () => {
  const endpoints = parseReportingEndpoints(
    ['default="https://first.example/collect"', 'default="https://second.example/collect"'],
    BASE,
  );
  assert.equal(endpoints.get("default")?.href, "https://first.example/collect");
});
