import { test } from "node:test";
import assert from "node:assert/strict";
import {
  guardResponse,
  guardMultipart,
  decideDisposition,
  sniffType,
  canonicalMime,
  TypeGuardError,
} from "../src/index.ts";

function bytes(...values: number[]): Uint8Array {
  return new Uint8Array(values);
}

test("nosniff header is always attached", () => {
  for (const [mime, ext] of [
    ["image/png", "pic.png"],
    ["text/html", "page.html"],
    ["application/octet-stream", "blob.bin"],
  ] as const) {
    const headers = guardResponse({ declaredType: mime, filename: ext });
    assert.equal(headers["x-content-type-options"], "nosniff");
    assert.equal(headers["content-type"], mime);
  }
});

test("interpretable types are forced to attachment", () => {
  const cases: Array<[string, string]> = [
    ["text/html", "page.html"],
    ["application/xhtml+xml", "page.xhtml"],
    ["image/svg+xml", "icon.svg"],
    ["application/pdf", "doc.pdf"],
    ["text/javascript", "app.js"],
    ["text/css", "style.css"],
    ["application/xml", "data.xml"],
  ];
  for (const [mime, filename] of cases) {
    const headers = guardResponse({ declaredType: mime, filename });
    assert.match(headers["content-disposition"], /^attachment/, `${mime} must download`);
    assert.equal(decideDisposition(mime).reason, "interpretable");
  }
});

test("executable types are forced to attachment", () => {
  const cases: Array<[string, string]> = [
    ["application/x-msdownload", "setup.exe"],
    ["application/x-elf", "tool.elf"],
    ["application/x-sh", "deploy.sh"],
    ["application/java-vm", "Main.class"],
    ["application/octet-stream", "payload.bin"],
  ];
  for (const [mime, filename] of cases) {
    const headers = guardResponse({ declaredType: mime, filename });
    assert.match(headers["content-disposition"], /^attachment/, `${mime} must download`);
  }
  assert.equal(decideDisposition("application/x-msdownload").reason, "executable");
  assert.equal(decideDisposition("application/octet-stream").reason, "unknown-binary");
});

test("safe inert types stay inline", () => {
  const headers = guardResponse({ declaredType: "image/png", filename: "pic.png" });
  assert.match(headers["content-disposition"], /^inline/);
  assert.equal(decideDisposition("image/png").reason, "safe");
});

test("matching extension and magic bytes pass the guard", () => {
  const png = bytes(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a);
  const headers = guardResponse({
    declaredType: "image/png",
    filename: "logo.png",
    body: png,
  });
  assert.equal(headers["content-type"], "image/png");
});

test("zip-family containers share the PK signature across declared formats", () => {
  const jar = bytes(0x50, 0x4b, 0x03, 0x04, 0x14, 0x00);
  const headers = guardResponse({
    declaredType: "application/java-archive",
    filename: "lib.jar",
    body: jar,
  });
  assert.match(headers["content-disposition"], /^inline/);
  assert.equal(sniffType(jar), "application/zip");
});

test("octet-stream accepts any binary signature but still downloads", () => {
  const pe = bytes(0x4d, 0x5a, 0x90, 0x00);
  const headers = guardResponse({
    declaredType: "application/octet-stream",
    filename: "firmware.bin",
    body: pe,
  });
  assert.match(headers["content-disposition"], /^attachment/);
});

test("MIME parameters and aliases are canonicalized", () => {
  assert.equal(canonicalMime("Text/HTML; charset=utf-8"), "text/html");
  assert.equal(canonicalMime("application/x-pdf"), "application/pdf");
  const headers = guardResponse({
    declaredType: "application/pdf; filename=report.pdf",
    filename: "report.pdf",
  });
  assert.equal(headers["content-type"], "application/pdf");
});

test("non-ASCII and path-bearing filenames are sanitized in disposition", () => {
  const headers = guardResponse({
    declaredType: "image/png",
    filename: "/var/folders/报告 图.png",
  });
  const disposition = headers["content-disposition"];
  assert.match(disposition, /^inline; filename="/);
  assert.match(disposition, /filename\*=UTF-8''/);
  assert.equal(disposition.includes("/"), false);
  assert.match(disposition, /%E6%8A%A5%E5%91%8A/);
});

test("each multipart part carries its own nosniff and disposition", () => {
  const output = guardMultipart([
    { name: "html", declaredType: "text/html", filename: "a.html" },
    { name: "json", declaredType: "application/json", filename: "b.json" },
    { name: "png", declaredType: "image/png", filename: "c.png" },
  ]);
  assert.equal(output.headers["x-content-type-options"], "nosniff");
  for (const part of output.parts) {
    assert.equal(part.headers["x-content-type-options"], "nosniff");
  }
  assert.match(output.parts[0]!.headers["content-disposition"], /^attachment/);
  assert.match(output.parts[1]!.headers["content-disposition"], /^inline/);
  assert.match(output.parts[2]!.headers["content-disposition"], /^inline/);
});

test("declared type drives everything: identical body, different declared types", () => {
  const text = new TextEncoder().encode("plain safe content");
  const safe = guardResponse({
    declaredType: "text/plain",
    filename: "notes.txt",
    body: text,
  });
  assert.match(safe["content-disposition"], /^inline/);

  const html = guardResponse({
    declaredType: "text/html",
    filename: "page.html",
    body: new TextEncoder().encode("<p>html</p>"),
  });
  assert.match(html["content-disposition"], /^attachment/);
});

test("leading text bodies pass only for declared textish types", () => {
  const body = new TextEncoder().encode("name,value\n1,2");
  const headers = guardResponse({
    declaredType: "text/plain",
    filename: "data.csv",
    body,
  });
  assert.equal(headers["content-type"], "text/plain");

  assert.throws(
    () =>
      guardResponse({
        declaredType: "image/png",
        filename: "pic.png",
        body,
      }),
    (error: unknown) =>
      error instanceof TypeGuardError && error.code === "content-sniff-mismatch",
  );
});

test("empty bodies cannot be sniffed and rely on the declared type", () => {
  const headers = guardResponse({
    declaredType: "image/png",
    filename: "empty.png",
    body: new Uint8Array(0),
  });
  assert.equal(headers["x-content-type-options"], "nosniff");
});

test("control characters and backslashes are stripped from filenames", () => {
  const headers = guardResponse({
    declaredType: "application/json",
    filename: "C:\\users\\evil\\name\u0000.json",
  });
  const disposition = headers["content-disposition"];
  assert.equal(disposition.includes("\\"), false);
  assert.equal(disposition.includes("\u0000"), false);
  assert.match(disposition, /filename="name_.json"/);
});
