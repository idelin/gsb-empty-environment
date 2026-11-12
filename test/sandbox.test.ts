import { test } from "node:test";
import assert from "node:assert/strict";

import {
  isOpaque,
  sameOrigin,
  tupleOrigin,
} from "../index.ts";
import { openDocument } from "../src/sandbox.ts";

test("非沙箱文档的来源由 URL 元组决定，默认端口被规范化", () => {
  const page = openDocument({ url: "https://shop.example.test/", sandboxed: false });
  assert.deepEqual(page.origin, tupleOrigin("https", "shop.example.test", 443));
  assert.equal(page.sandboxed, false);
});

test("沙箱文档获得唯一不透明身份，两个独立沙箱互不同源", () => {
  const sandboxOne = openDocument({
    url: "https://shop.example.test/widget",
    sandboxed: true,
  });
  const sandboxTwo = openDocument({
    url: "https://shop.example.test/widget",
    sandboxed: true,
  });

  assert.ok(isOpaque(sandboxOne.origin));
  assert.ok(isOpaque(sandboxTwo.origin));
  assert.equal(false, sameOrigin(sandboxOne.origin, sandboxTwo.origin));
  assert.equal(false, sameOrigin(sandboxOne.origin, tupleOrigin("https", "shop.example.test")));
});

test("同一沙箱内 about:blank 继承父文档的不透明身份，跨沙箱不继承", () => {
  const parent = openDocument({
    url: "https://shop.example.test/frame",
    sandboxed: true,
  });
  const childBlank = openDocument({
    url: "about:blank",
    sandboxed: true,
    opener: parent,
  });
  const grandChildBlank = openDocument({
    url: "about:blank",
    sandboxed: true,
    opener: childBlank,
  });

  assert.ok(sameOrigin(parent.origin, childBlank.origin));
  assert.ok(sameOrigin(childBlank.origin, grandChildBlank.origin));

  const foreignParent = openDocument({
    url: "https://other.example.test/frame",
    sandboxed: true,
  });
  const foreignSandbox = openDocument({
    url: "about:blank",
    sandboxed: true,
    opener: foreignParent,
  });
  assert.equal(false, sameOrigin(parent.origin, foreignSandbox.origin));
});

test("非沙箱 about:blank 继承普通来源的结构副本而非共享引用", () => {
  const parent = openDocument({ url: "http://app.example.test:8080", sandboxed: false });
  const blank = openDocument({ url: "about:blank", sandboxed: false, opener: parent });
  assert.ok(sameOrigin(parent.origin, blank.origin));
  assert.notEqual(parent.origin, blank.origin);
});
