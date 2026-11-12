import assert from "node:assert/strict";
import { test } from "node:test";
import { normalizeHref } from "../src/href.ts";
import { XIncludeSecurityError } from "../src/errors.ts";

test("同包相对路径按当前文档目录解析", () => {
  assert.equal(normalizeHref("b.xml", "frag/a.xml"), "frag/b.xml");
  assert.equal(normalizeHref("../b.xml", "frag/sub/a.xml"), "frag/b.xml");
  assert.equal(normalizeHref("./b.xml", "a.xml"), "b.xml");
  assert.equal(
    normalizeHref("sub/../b.xml", "frag/a.xml"),
    "frag/b.xml",
  );
});

test("拒绝绝对文件系统路径", () => {
  assert.throws(
    () => normalizeHref("/etc/passwd", "a.xml"),
    XIncludeSecurityError,
  );
  assert.throws(
    () => normalizeHref("//host/share", "a.xml"),
    XIncludeSecurityError,
  );
});

test("拒绝 file: 及其它绝对 URI scheme", () => {
  for (const href of [
    "file:///etc/passwd",
    "file:/etc/passwd",
    "http://evil.test/x.xml",
    "jar:file:/x.jar!/x.xml",
  ]) {
    assert.throws(
      () => normalizeHref(href, "a.xml"),
      XIncludeSecurityError,
      href,
    );
  }
});

test("拒绝反斜杠、百分号编码、片段与控制字符", () => {
  for (const href of [
    "..\\..\\etc\\passwd",
    "%2e%2e%2fetc",
    "a.xml#frag",
    "a\nb.xml",
  ]) {
    assert.throws(
      () => normalizeHref(href, "a.xml"),
      XIncludeSecurityError,
      href,
    );
  }
});

test("拒绝 Windows 盘符与 UNC 路径", () => {
  for (const href of ["C:/windows/win.ini", "c:\\temp\\x.xml", "//server/share"]) {
    assert.throws(
      () => normalizeHref(href, "a.xml"),
      XIncludeSecurityError,
      href,
    );
  }
});

test("点段越出包根时拒绝", () => {
  assert.throws(
    () => normalizeHref("../../etc/passwd", "a.xml"),
    XIncludeSecurityError,
  );
  assert.throws(
    () => normalizeHref("../escape.xml", "a.xml"),
    XIncludeSecurityError,
  );
});

test("空 href 与非资源 href 被拒绝", () => {
  assert.throws(() => normalizeHref("", "a.xml"), XIncludeSecurityError);
  assert.throws(() => normalizeHref("   ", "a.xml"), XIncludeSecurityError);
  assert.throws(() => normalizeHref(".", "a.xml"), XIncludeSecurityError);
});
