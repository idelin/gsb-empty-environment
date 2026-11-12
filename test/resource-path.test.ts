import assert from "node:assert/strict";
import test from "node:test";

import {
  ResourcePathRejectedError,
  assertResourcePathAllowed,
  inspectResourcePath,
  isResourcePathAllowed,
} from "../index.ts";

test("allows already-normalized public paths", () => {
  const decision = inspectResourcePath("/public/assets/logo.svg");

  assert.equal(decision.allowed, true);
  assert.equal(decision.reason, null);
  assert.equal(decision.nfc, "/public/assets/logo.svg");
  assert.equal(decision.nfkc, "/public/assets/logo.svg");
  assert.equal(isResourcePathAllowed("/public/assets/logo.svg"), true);
});

test("allows canonical and decomposed public paths", () => {
  const canonical = inspectResourcePath("/public/éclair/照片.png");
  const decomposed = inspectResourcePath("/public/cafe\u0301/photo.png");

  assert.equal(canonical.allowed, true);
  assert.equal(decomposed.allowed, true);
  assert.equal(decomposed.nfc, "/public/café/photo.png");
  assert.equal(decomposed.nfkc, "/public/café/photo.png");
});

test("allows valid Chinese filenames", () => {
  assert.equal(
    isResourcePathAllowed("/assets/产品手册/封面图-2026.png"),
    true,
  );
  assert.equal(
    isResourcePathAllowed("/public/日本語/ドキュメント.pdf"),
    true,
  );
});

test("allows valid non-BMP Unicode filenames", () => {
  assert.equal(isResourcePathAllowed("/public/😀-表情.png"), true);
});

test("rejects an unpaired high surrogate", () => {
  const decision = inspectResourcePath("/public/\ud800/file.png");

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, "invalid-unicode");
  assert.equal(decision.nfc, null);
  assert.equal(decision.nfkc, null);
});

test("rejects an unpaired low surrogate", () => {
  const decision = inspectResourcePath("/public/\udc00/file.png");

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, "invalid-unicode");
});

test("rejects empty, NUL-containing, and non-string input", () => {
  assert.equal(inspectResourcePath("").reason, "invalid-input");
  assert.equal(
    inspectResourcePath("/public/file\0.png").reason,
    "invalid-input",
  );
  assert.equal(
    inspectResourcePath(null as unknown as string).reason,
    "invalid-input",
  );
});

test("rejects fullwidth slashes by NFKC protected-prefix detection", () => {
  const decision = inspectResourcePath("／private／secret.txt");

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, "protected-prefix");
  assert.equal(decision.nfc, "／private／secret.txt");
  assert.equal(decision.nfkc, "/private/secret.txt");
});

test("rejects fullwidth letters that NFKC turns into the protected prefix", () => {
  const decision = inspectResourcePath("/ｐｒｉｖａｔｅ/secret.txt");

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, "protected-prefix");
  assert.equal(decision.nfkc, "/private/secret.txt");
});

test("rejects protected paths that use mixed separators", () => {
  assert.equal(
    isResourcePathAllowed("/private／secret.txt"),
    false,
  );
  assert.equal(
    isResourcePathAllowed("/private/../public/file.txt"),
    false,
  );
  assert.equal(isResourcePathAllowed("/private"), false);
});

test("rejects separator changes outside the protected prefix", () => {
  const decision = inspectResourcePath("／public／logo.svg");

  assert.equal(decision.allowed, false);
  assert.equal(decision.reason, "separator-mismatch");
  assert.equal(decision.nfkc, "/public/logo.svg");

  assert.equal(
    inspectResourcePath("/public／logo.svg").reason,
    "separator-mismatch",
  );
});

test("rejects fullwidth dot segments after NFKC", () => {
  const singleDot = inspectResourcePath("/public/．/index.html");
  const doubleDot = inspectResourcePath("/public/．．/private/file.txt");

  assert.equal(singleDot.allowed, false);
  assert.equal(singleDot.reason, "dot-segment");
  assert.equal(singleDot.nfkc, "/public/./index.html");

  assert.equal(doubleDot.allowed, false);
  assert.equal(doubleDot.reason, "dot-segment");
  assert.equal(doubleDot.nfkc, "/public/../private/file.txt");
});

test("rejects ASCII dot segments in both normalized forms", () => {
  assert.equal(
    inspectResourcePath("/public/./file.txt").reason,
    "dot-segment",
  );
  assert.equal(
    inspectResourcePath("/public/../private/file.txt").reason,
    "dot-segment",
  );
  assert.equal(
    inspectResourcePath("./private/file.txt").reason,
    "dot-segment",
  );
  assert.equal(
    isResourcePathAllowed(".／private／file.txt"),
    false,
  );
});

test("does not reject names that merely start with private", () => {
  assert.equal(isResourcePathAllowed("/private-ish/file.txt"), true);
  assert.equal(
    isResourcePathAllowed("/public/private/file.txt"),
    true,
  );
});

test("assertResourcePathAllowed returns safe paths and throws on rejection", () => {
  assert.equal(
    assertResourcePathAllowed("/public/图片.png"),
    "/public/图片.png",
  );

  assert.throws(
    () => assertResourcePathAllowed("/public/../private/secret.txt"),
    (error: unknown) =>
      error instanceof ResourcePathRejectedError &&
      error.reason === "dot-segment",
  );
});
