import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  RegistrationSecurityError,
  compareScopes,
  isPathPrefix,
  normalizeUrl,
  sameOrigin,
  scopeContains,
  scopeFromUrl,
  scriptDirectory,
} from '../src/index.ts';

describe('normalizeUrl 路径段规范化', () => {
  it('按段拆分并解析 . 与 ..', () => {
    const url = normalizeUrl('https://app.example.com/a/b/../c/./sw.js');
    assert.deepEqual(url.segments, ['a', 'c', 'sw.js']);
    assert.equal(url.pathname, '/a/c/sw.js');
    assert.equal(url.origin, 'https://app.example.com');
  });

  it('丢弃查询串与片段，保证决策只基于路径段', () => {
    const url = normalizeUrl('https://app.example.com/a/sw.js?x=1#frag');
    assert.deepEqual(url.segments, ['a', 'sw.js']);
  });

  it('相对 URL 基于客户 URL 解析', () => {
    const url = normalizeUrl('sw.js', 'https://app.example.com/a/page');
    assert.equal(url.pathname, '/a/sw.js');
  });

  it('拒绝非 http(s) 方案', () => {
    assert.throws(
      () => normalizeUrl('file:///etc/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError && error.code === 'UNSUPPORTED_SCHEME',
    );
  });

  it('拒绝无法解析的 URL', () => {
    assert.throws(
      () => normalizeUrl('not a url'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError && error.code === 'INVALID_URL',
    );
  });
});

describe('编码分隔符歧义', () => {
  it('拒绝编码斜杠 %2f', () => {
    assert.throws(
      () => normalizeUrl('https://app.example.com/a%2fb/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'ENCODED_SEPARATOR_AMBIGUITY',
    );
  });

  it('拒绝大写编码斜杠 %2F', () => {
    assert.throws(
      () => normalizeUrl('https://app.example.com/a%2Fb/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'ENCODED_SEPARATOR_AMBIGUITY',
    );
  });

  it('拒绝编码反斜杠 %5c', () => {
    assert.throws(
      () => normalizeUrl('https://app.example.com/a%5Cb/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'ENCODED_SEPARATOR_AMBIGUITY',
    );
  });

  it('拒绝原始反斜杠', () => {
    assert.throws(
      () => normalizeUrl('https://app.example.com/a\\b/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'ENCODED_SEPARATOR_AMBIGUITY',
    );
  });

  it('拒绝非法百分号转义', () => {
    assert.throws(
      () => normalizeUrl('https://app.example.com/a%2/sw.js'),
      (error: unknown) =>
        error instanceof RegistrationSecurityError &&
        error.code === 'INVALID_PERCENT_ENCODING',
    );
  });

  it('允许不含分隔符含义的合法编码', () => {
    const url = normalizeUrl('https://app.example.com/a%20b/sw.js');
    assert.deepEqual(url.segments, ['a%20b', 'sw.js']);
  });
});

describe('来源与作用域段比较', () => {
  it('sameOrigin 区分端口', () => {
    assert.equal(
      sameOrigin(
        normalizeUrl('https://app.example.com:8443/a/sw.js'),
        normalizeUrl('https://app.example.com/a/sw.js'),
      ),
      false,
    );
  });

  it('scriptDirectory 去掉文件名段', () => {
    const dir = scriptDirectory(normalizeUrl('https://app.example.com/a/b/sw.js'));
    assert.deepEqual(dir.scopeSegments, ['a', 'b']);
    assert.equal(dir.scopePath, '/a/b/');
  });

  it('scopeFromUrl 以斜杠结尾的目录作用域', () => {
    const scope = scopeFromUrl(normalizeUrl('https://app.example.com/a/b/'));
    assert.deepEqual(scope.scopeSegments, ['a', 'b']);
  });

  it('段前缀匹配不受字符串前缀欺骗', () => {
    const scope = scopeFromUrl(normalizeUrl('https://app.example.com/app'));
    const outside = normalizeUrl('https://app.example.com/application/x');
    const inside = normalizeUrl('https://app.example.com/app/page');
    assert.equal(scopeContains(scope, outside), false);
    assert.equal(scopeContains(scope, inside), true);
  });

  it('isPathPrefix 处理长度边界', () => {
    assert.equal(isPathPrefix(['a', 'b'], ['a']), false);
    assert.equal(isPathPrefix([], ['a']), true);
  });

  it('compareScopes 分类四种关系', () => {
    const root = scopeFromUrl(normalizeUrl('https://app.example.com/'));
    const app = scopeFromUrl(normalizeUrl('https://app.example.com/app/'));
    const other = scopeFromUrl(normalizeUrl('https://app.example.com/other/'));
    assert.equal(compareScopes(app, app), 'equal');
    assert.equal(compareScopes(root, app), 'contains');
    assert.equal(compareScopes(app, root), 'contained-by');
    assert.equal(compareScopes(app, other), 'disjoint');
  });
});
