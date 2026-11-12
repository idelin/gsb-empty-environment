import { test } from "node:test";
import assert from "node:assert/strict";
import { PreloadReuseChecker } from "../index.ts";

const PAGE = "https://page.example";
const CDN_SCRIPT = "https://cdn.example/app.js";
const CDN_STYLE = "https://cdn.example/site.css";
const CDN_IMG = "https://cdn.example/logo.png";
const CDN_FONT = "https://cdn.example/font.woff2";

function makeChecker(now: () => number = () => 1_000) {
  return new PreloadReuseChecker({ pageOrigin: PAGE, now });
}

test("终态 CSP 放行时复用提前抓取的条目", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.handleFinalResponse({ status: 200, csp: "script-src https://cdn.example; default-src 'self'" });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: true });
  // 条目被消费，再次判定应重新抓取
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "no-entry" });
});

test("CSP 拒绝对应指令时丢弃并重新抓取", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.addHint({ url: CDN_IMG, as: "image" });
  c.handleFinalResponse({
    status: 200,
    csp: "script-src 'self'; img-src https://cdn.example",
  });
  // script-src 不含 cdn.example → 拒绝
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "csp-blocked" });
  // img-src 放行 → 复用
  assert.deepEqual(c.decide({ url: CDN_IMG, as: "image" }), { reuse: true });
});

test("as 类型映射到各自指令：style-src / font-src", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_STYLE, as: "style" });
  c.addHint({ url: CDN_FONT, as: "font" });
  c.handleFinalResponse({
    status: 200,
    csp: "default-src 'none'; style-src https://cdn.example; font-src 'self'",
  });
  assert.deepEqual(c.decide({ url: CDN_STYLE, as: "style" }), { reuse: true });
  assert.deepEqual(c.decide({ url: CDN_FONT, as: "font" }), { reuse: false, reason: "csp-blocked" });
});

test("专用指令缺失时回退 default-src", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.handleFinalResponse({ status: 200, csp: "default-src 'self'" });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "csp-blocked" });
});

test("crossorigin 模式与标签不一致时丢弃", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_FONT, as: "font", crossorigin: "anonymous" });
  c.handleFinalResponse({ status: 200, csp: "font-src *" });
  // 标签未声明 crossorigin，条目是 anonymous → 不一致
  assert.deepEqual(c.decide({ url: CDN_FONT, as: "font" }), {
    reuse: false,
    reason: "crossorigin-mismatch",
  });

  const c2 = makeChecker();
  c2.addHint({ url: CDN_FONT, as: "font", crossorigin: "use-credentials" });
  c2.handleFinalResponse({ status: 200, csp: "font-src *" });
  assert.deepEqual(c2.decide({ url: CDN_FONT, as: "font", crossorigin: "anonymous" }), {
    reuse: false,
    reason: "crossorigin-mismatch",
  });

  const c3 = makeChecker();
  c3.addHint({ url: CDN_FONT, as: "font", crossorigin: "anonymous" });
  c3.handleFinalResponse({ status: 200, csp: "font-src *" });
  assert.deepEqual(c3.decide({ url: CDN_FONT, as: "font", crossorigin: "anonymous" }), { reuse: true });
});

test("完整性元数据与标签不一致时丢弃", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script", integrity: "sha384-aaa" });
  c.handleFinalResponse({ status: 200, csp: "script-src *" });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script", integrity: "sha384-bbb" }), {
    reuse: false,
    reason: "integrity-mismatch",
  });

  const c2 = makeChecker();
  c2.addHint({ url: CDN_SCRIPT, as: "script", integrity: "sha384-aaa" });
  c2.handleFinalResponse({ status: 200, csp: "script-src *" });
  // 标签没有 integrity，条目有 → 不一致
  assert.deepEqual(c2.decide({ url: CDN_SCRIPT, as: "script" }), {
    reuse: false,
    reason: "integrity-mismatch",
  });

  const c3 = makeChecker();
  c3.addHint({ url: CDN_SCRIPT, as: "script", integrity: "sha384-aaa" });
  c3.handleFinalResponse({ status: 200, csp: "script-src *" });
  assert.deepEqual(c3.decide({ url: CDN_SCRIPT, as: "script", integrity: "sha384-aaa" }), { reuse: true });
});

test("终态为重定向时清空全部条目", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.addHint({ url: CDN_IMG, as: "image" });
  c.handleFinalResponse({ status: 200, redirected: true, csp: "default-src *" });
  assert.equal(c.size, 0);
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "no-entry" });
});

test("终态为非 2xx 时清空全部条目", () => {
  for (const status of [301, 404, 500]) {
    const c = makeChecker();
    c.addHint({ url: CDN_SCRIPT, as: "script" });
    c.handleFinalResponse({ status, csp: "default-src *" });
    assert.equal(c.size, 0);
    assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "no-entry" });
  }
});

test("条目超过 30 秒未使用被丢弃", () => {
  let now = 1_000;
  const c = makeChecker(() => now);
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.handleFinalResponse({ status: 200, csp: "script-src *" });
  now = 1_000 + 29_999;
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: true });

  const c2 = makeChecker(() => now);
  now = 1_000;
  c2.addHint({ url: CDN_SCRIPT, as: "script" });
  c2.handleFinalResponse({ status: 200, csp: "script-src *" });
  now = 1_000 + 30_001;
  assert.deepEqual(c2.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "expired" });
  assert.equal(c2.size, 0);
});

test("终态未到达前不复用", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), {
    reuse: false,
    reason: "no-final-response",
  });
});

test("无 CSP 头时不限制复用", () => {
  const c = makeChecker();
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.handleFinalResponse({ status: 200 });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: true });
});

test("'self' 与通配符源匹配", () => {
  const c = makeChecker();
  c.addHint({ url: `${PAGE}/local.js`, as: "script" });
  c.addHint({ url: CDN_SCRIPT, as: "script" });
  c.handleFinalResponse({ status: 200, csp: "script-src 'self'" });
  assert.deepEqual(c.decide({ url: `${PAGE}/local.js`, as: "script" }), { reuse: true });
  assert.deepEqual(c.decide({ url: CDN_SCRIPT, as: "script" }), { reuse: false, reason: "csp-blocked" });
});
