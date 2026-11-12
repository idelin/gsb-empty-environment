import { test } from "node:test";
import assert from "node:assert/strict";
import { parseReportingEndpoints } from "../src/endpoints.ts";

const PUBLIC_DOC = "https://site.example/page";

test("parses declared endpoints and resolves relative URLs against the response URL", () => {
  const endpoints = parseReportingEndpoints(
    'default="https://reports.example.com/r", local="/csp-reports?x=1"',
    "https://site.example/docs/page",
  );
  assert.equal(endpoints.get("default"), "https://reports.example.com/r");
  assert.equal(endpoints.get("local"), "https://site.example/csp-reports?x=1");
});

test("ignores the whole header when the dictionary fails to parse", () => {
  const endpoints = parseReportingEndpoints(
    'good="https://ok.example/", bad=?1',
    PUBLIC_DOC,
  );
  assert.equal(endpoints.size, 0);
});

test("drops non-HTTPS endpoints", () => {
  const endpoints = parseReportingEndpoints(
    'a="http://reports.example.com/", b="ftp://reports.example.com/"',
    PUBLIC_DOC,
  );
  assert.equal(endpoints.size, 0);
});

test("drops endpoints carrying userinfo", () => {
  const endpoints = parseReportingEndpoints(
    'a="https://user@reports.example.com/", b="https://user:pass@reports.example.com/"',
    PUBLIC_DOC,
  );
  assert.equal(endpoints.size, 0);
});

test("drops loopback, private, and link-local endpoints from a public document", () => {
  const malicious = [
    "https://127.0.0.1/",
    "https://127.1/",
    "https://[::1]/",
    "https://10.0.0.5/",
    "https://172.16.0.1/",
    "https://172.31.255.255/",
    "https://192.168.1.1/",
    "https://169.254.169.254/latest/meta-data",
    "https://[fe80::1]/",
    "https://[fd00::1]/",
  ];
  for (const url of malicious) {
    const endpoints = parseReportingEndpoints(`a="${url}"`, PUBLIC_DOC);
    assert.equal(endpoints.size, 0, `expected ${url} to be dropped`);
  }
});

test("drops integer and obfuscated IP spellings of internal addresses", () => {
  const malicious = [
    "https://2130706433/", // 127.0.0.1 as decimal integer
    "https://0x7f000001/", // 127.0.0.1 as hex integer
    "https://0177.0.0.1/", // 127.0.0.1 with octal part
    "https://0x7f.1/", // mixed hex + short form
    "https://3232235521/", // 192.168.0.1 as decimal integer
    "https://0xc0a80001/", // 192.168.0.1 as hex integer
    "https://2852039166/", // 169.254.169.254 as decimal integer
    "https://[::ffff:127.0.0.1]/", // IPv4-mapped IPv6 loopback
    "https://[::ffff:7f00:1]/", // IPv4-mapped IPv6, hex groups
    "https://[::ffff:192.168.0.1]/", // IPv4-mapped IPv6 private
    "https://[::ffff:a9fe:a9fe]/", // IPv4-mapped IPv6 link-local
  ];
  for (const url of malicious) {
    const endpoints = parseReportingEndpoints(`a="${url}"`, PUBLIC_DOC);
    assert.equal(endpoints.size, 0, `expected ${url} to be dropped`);
  }
});

test("allows internal endpoints when the document is in the same address space", () => {
  const cases: Array<[string, string, string]> = [
    ["https://192.168.1.5/admin", "https://192.168.1.10/reports", "https://192.168.1.10/reports"],
    ["https://10.1.2.3/", "https://10.9.9.9/r", "https://10.9.9.9/r"],
    ["https://127.0.0.1:8080/", "https://127.0.0.1:9090/r", "https://127.0.0.1:9090/r"],
    ["https://[::1]/", "https://[::1]:8443/r", "https://[::1]:8443/r"],
    ["https://169.254.0.1/", "https://169.254.0.2/r", "https://169.254.0.2/r"],
    ["https://[fd00::5]/", "https://[fd00::6]/r", "https://[fd00::6]/r"],
  ];
  for (const [doc, endpoint, expected] of cases) {
    const endpoints = parseReportingEndpoints(`a="${endpoint}"`, doc);
    assert.equal(endpoints.get("a"), expected, `from ${doc}`);
  }
});

test("blocks cross-space internal delivery (private doc to loopback, etc.)", () => {
  const cases: Array<[string, string]> = [
    ["https://192.168.1.5/", "https://127.0.0.1/"],
    ["https://127.0.0.1/", "https://192.168.1.10/"],
    ["https://10.0.0.1/", "https://169.254.169.254/"],
    ["https://[::1]/", "https://[fe80::1]/"],
  ];
  for (const [doc, endpoint] of cases) {
    const endpoints = parseReportingEndpoints(`a="${endpoint}"`, doc);
    assert.equal(endpoints.size, 0, `from ${doc} to ${endpoint}`);
  }
});

test("keeps public endpoints and drops invalid ones individually", () => {
  const endpoints = parseReportingEndpoints(
    'good="https://reports.example.com/", bad="http://insecure.example.com/"',
    PUBLIC_DOC,
  );
  assert.equal(endpoints.size, 1);
  assert.equal(endpoints.get("good"), "https://reports.example.com/");
});

test("public IPv4/IPv6 endpoints are kept", () => {
  const endpoints = parseReportingEndpoints(
    'a="https://8.8.8.8/r", b="https://[2606:4700:4700::1111]/r"',
    PUBLIC_DOC,
  );
  assert.equal(endpoints.get("a"), "https://8.8.8.8/r");
  assert.equal(endpoints.get("b"), "https://[2606:4700:4700::1111]/r");
});
