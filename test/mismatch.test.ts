import { test } from "node:test";
import assert from "node:assert/strict";
import { guardResponse, TypeGuardError } from "../src/index.ts";

function bytes(...values: number[]): Uint8Array {
  return new Uint8Array(values);
}

function expectCode(code: string, fn: () => unknown): TypeGuardError {
  assert.throws(
    fn,
    (error: unknown) => error instanceof TypeGuardError && error.code === code,
    `expected TypeGuardError with code ${code}`,
  );
  // Re-run to obtain the error object for detail assertions.
  try {
    fn();
  } catch (error) {
    return error as TypeGuardError;
  }
  throw new Error("unreachable");
}

test("mismatch class 1: extension conflict with declared type is rejected", () => {
  const error = expectCode("extension-mismatch", () =>
    guardResponse({
      declaredType: "image/png",
      filename: "report.exe",
      body: bytes(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a),
    }),
  );
  assert.equal(error.detail.extension, "exe");
  assert.equal(error.detail.declared, "image/png");
});

test("mismatch class 1: extension implying another MIME type is rejected", () => {
  expectCode("extension-mismatch", () =>
    guardResponse({ declaredType: "text/plain", filename: "notes.html" }),
  );
});

test("mismatch class 1: missing filename and missing extension fail closed", () => {
  expectCode("extension-mismatch", () =>
    guardResponse({ declaredType: "application/pdf" }),
  );
  expectCode("extension-mismatch", () =>
    guardResponse({ declaredType: "application/pdf", filename: "report" }),
  );
});

test("mismatch class 1: unknown declared type has no known extension mapping", () => {
  expectCode("extension-mismatch", () =>
    guardResponse({ declaredType: "application/x-unknown-vendor", filename: "file.xyz" }),
  );
});

test("mismatch class 2: HTML magic bytes under a plain-text declaration are sniffed", () => {
  const html = new TextEncoder().encode("<!DOCTYPE html><script>alert(1)</script>");
  const error = expectCode("content-sniff-mismatch", () =>
    guardResponse({ declaredType: "text/plain", filename: "notes.txt", body: html }),
  );
  assert.equal(error.detail.sniffed, "text/html");
  assert.equal(error.detail.declared, "text/plain");
});

test("mismatch class 2: PE executable hidden as PNG is sniffed", () => {
  const pe = bytes(0x4d, 0x5a, 0x90, 0x00, 0x03, 0x00);
  const error = expectCode("content-sniff-mismatch", () =>
    guardResponse({ declaredType: "image/png", filename: "picture.png", body: pe }),
  );
  assert.equal(error.detail.sniffed, "application/x-dosexec");
});

test("mismatch class 2: ELF binary hidden as octet-stream text claim is sniffed", () => {
  const elf = bytes(0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01);
  expectCode("content-sniff-mismatch", () =>
    guardResponse({ declaredType: "text/plain", filename: "load.txt", body: elf }),
  );
});

test("mismatch class 2: SVG payload under image/png is sniffed", () => {
  const svg = new TextEncoder().encode('<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>');
  expectCode("content-sniff-mismatch", () =>
    guardResponse({ declaredType: "image/png", filename: "icon.png", body: svg }),
  );
});

test("mismatch class 3: inline upstream disposition for HTML must be attachment", () => {
  const html = new TextEncoder().encode("<html></html>");
  const error = expectCode("disposition-mismatch", () =>
    guardResponse({
      declaredType: "text/html",
      filename: "page.html",
      body: html,
      headers: { "Content-Disposition": "inline; filename=page.html" },
    }),
  );
  assert.equal(error.detail.actual, "inline");
  assert.equal(error.detail.required, "attachment");
});

test("mismatch class 3: inline upstream disposition for executable is rejected", () => {
  expectCode("disposition-mismatch", () =>
    guardResponse({
      declaredType: "application/x-msdownload",
      filename: "setup.exe",
      headers: { "content-disposition": "inline" },
    }),
  );
});

test("mismatch class 3: attachment request for an inline-safe type is rejected upstream", () => {
  expectCode("disposition-mismatch", () =>
    guardResponse({
      declaredType: "image/png",
      filename: "pic.png",
      headers: { "Content-Disposition": "attachment; filename=pic.png" },
    }),
  );
});
