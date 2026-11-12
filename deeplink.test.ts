import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  validateDeepLink,
  toAsciiHost,
  punycodeEncode,
  normalizePath,
} from './deeplink.ts';

const WHITELIST = ['open', 'pay', 'app.example.com', 'xn--r8jz45g.jp'] as const;

const ok = (raw: string) => {
  const r = validateDeepLink(raw, WHITELIST);
  assert.ok(r.ok, `期望通过但被拒绝: ${raw} -> ${(r as { reason?: string }).reason}`);
  return (r as { ok: true; url: string }).url;
};

const reject = (raw: string) => {
  const r = validateDeepLink(raw, WHITELIST);
  assert.ok(!r.ok, `期望拒绝但通过: ${raw} -> ${(r as { url?: string }).url}`);
};

/* ---------- 基础放行与规范化 ---------- */

test('合法 myapp 与 https 深链放行', () => {
  assert.equal(ok('myapp://open'), 'myapp://open');
  assert.equal(ok('https://app.example.com/home'), 'https://app.example.com/home');
});

test('大小写变形归一化后精确匹配白名单', () => {
  assert.equal(ok('MYAPP://OPEN'), 'myapp://open');
  assert.equal(ok('HTTPS://APP.EXAMPLE.COM/Path'), 'https://app.example.com/Path');
});

test('返回给导航层的是规范化结果', () => {
  assert.equal(
    ok('HTTPS://APP.EXAMPLE.COM/a/../%2e%2e/admin'),
    'https://app.example.com/admin',
  );
});

/* ---------- 用户信息 ---------- */

test('含用户信息的深链一律拒绝', () => {
  reject('myapp://pay@evil.com');
  reject('myapp://open@evil.com');
  reject('myapp://@open');
  reject('https://user:pass@app.example.com/');
  reject('https://app.example.com@evil.com/');
});

/* ---------- 反斜杠 ---------- */

test('反斜杠一律拒绝（含解码后出现的）', () => {
  reject('myapp:\\open');
  reject('https://app.example.com\\@evil.com/');
  reject('https:\\\\app.example.com/');
  reject('https://app.example.com/%5c..%5c/admin');
});

/* ---------- 控制字符与 NUL ---------- */

test('控制字符与 NUL 一律拒绝（含编码形式）', () => {
  reject('myapp://open\n');
  reject('myapp://open?url=https://app.example.com/\x00');
  reject('https://app.example.com/%00');
  reject('https://app.example.com/%0a%0d');
  reject('https://app.example.com/%7f');
});

/* ---------- scheme 与主机白名单 ---------- */

test('非白名单 scheme 拒绝', () => {
  reject('javascript:alert(1)');
  reject('ftp://app.example.com/');
  reject('file:///etc/passwd');
});

test('非白名单主机与末尾点变形拒绝', () => {
  reject('https://evil.com/');
  reject('https://app.example.com.evil.com/');
  reject('https://app.example.com./');
  reject('myapp://open2');
});

test('punycode 主机：白名单内放行，白名单外拒绝', () => {
  assert.equal(toAsciiHost('例え.jp'), 'xn--r8jz45g.jp');
  assert.equal(ok('https://例え.jp/'), 'https://xn--r8jz45g.jp/');
  reject('https://例え.com/');
});

test('punycodeEncode 与 RFC 3492 示例一致', () => {
  assert.equal(punycodeEncode('例え'), 'r8jz45g');
  assert.equal(punycodeEncode('bücher'), 'bcher-kva');
});

/* ---------- 路径：解码一次 + 点段消除 ---------- */

test('编码点段解码一次后消去', () => {
  assert.equal(normalizePath('/%2e%2e/admin'), '/admin');
  assert.equal(normalizePath('/a/./b/../c'), '/a/c');
  assert.equal(normalizePath('/a/%2e/b'), '/a/b');
});

test('双重编码只解码一次，不二次展开为点段', () => {
  const url = ok('https://app.example.com/%252e%252e/admin');
  assert.equal(url, 'https://app.example.com/%2e%2e/admin');
});

test('畸形百分号编码拒绝', () => {
  reject('https://app.example.com/%zz');
});

/* ---------- url 参数递归与嵌套上限 ---------- */

test('url 参数递归通过同一校验', () => {
  const inner = encodeURIComponent('https://app.example.com/home');
  assert.equal(
    ok(`myapp://open?url=${inner}`),
    'myapp://open?url=https%3A%2F%2Fapp.example.com%2Fhome',
  );
});

test('嵌套重定向到非白名单主机拒绝', () => {
  reject(`myapp://open?url=${encodeURIComponent('https://evil.com/')}`);
  reject(`myapp://open?url=${encodeURIComponent('myapp://pay@evil.com')}`);
});

test('嵌套结果同样被规范化后回写', () => {
  const inner = encodeURIComponent('HTTPS://APP.EXAMPLE.COM/a/../b');
  assert.equal(
    ok(`myapp://open?url=${inner}`),
    'myapp://open?url=https%3A%2F%2Fapp.example.com%2Fb',
  );
});

test('嵌套不超过 2 层', () => {
  const l2 = `myapp://open?url=${encodeURIComponent('https://app.example.com/')}`;
  assert.ok(validateDeepLink(l2, WHITELIST).ok);
  const l3 = `myapp://open?url=${encodeURIComponent(l2)}`;
  const r = validateDeepLink(l3, WHITELIST);
  assert.ok(!r.ok);
  assert.match(r.reason, /嵌套/);
});
