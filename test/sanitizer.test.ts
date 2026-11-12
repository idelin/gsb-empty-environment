import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { sanitizePaste } from '../src/sanitizer.ts';
import { isSafeUrl } from '../src/whitelist.ts';
import {
  HTML_NS,
  MATHML_NS,
  SVG_NS,
  type MiniElement,
  type MiniNode,
} from '../src/types.ts';
import { createTestDomHost, parseHTML, serialize } from './dom.ts';

const host = createTestDomHost();

function clean(html: string) {
  return sanitizePaste(parseHTML(html), host);
}

function elements(node: MiniNode): MiniElement[] {
  const result: MiniElement[] = [];
  const walk = (current: MiniNode): void => {
    if (host.isElement(current)) {
      result.push(current);
    }
    for (const child of current.childNodes) {
      walk(child);
    }
  };
  walk(node);
  return result;
}

describe('isSafeUrl 仅放行 http/https/mailto', () => {
  it('接受合法绝对 URL', () => {
    assert.equal(isSafeUrl('https://example.test/a?b=1#c'), true);
    assert.equal(isSafeUrl('http://example.test:8080/path'), true);
    assert.equal(isSafeUrl('mailto:user@example.test'), true);
  });

  it('拒绝危险 scheme 与混淆写法', () => {
    const dangerous = [
      'javascript:alert(1)',
      'JaVaScRiPt:alert(1)',
      'java\tscript:alert(1)',
      'java\nscript:alert(1)',
      'vbscript:msgbox(1)',
      'data:text/html,<script>alert(1)</script>',
      '//evil.test/x',
      '/relative/path',
      'relative/path',
      '#fragment',
      '?query=1',
      'javascript&colon;alert(1)',
      'mailto:',
      'https://',
      '   ',
      '',
    ];
    for (const url of dangerous) {
      assert.equal(isSafeUrl(url), false, url);
    }
  });

  it('剥离控制字符后再判定，避免归一化绕过', () => {
    assert.equal(isSafeUrl('https://example.test/a\tb'), true);
    assert.equal(isSafeUrl('java\tscript:alert(1)'), false);
  });
});

describe('白名单元素与属性过滤', () => {
  it('保留内容元素，剥离可执行结构与 style', () => {
    const result = clean(`
      <div data-id="x" style="color:red" onclick="alert(1)" class="keep?">
        <p>hello <strong>world</strong></p>
        <script>alert(1)</script>
        <style>body{color:red}</style>
        <iframe src="https://evil.test"></iframe>
        <a href="https://ok.test" onclick="steal()">link</a>
      </div>
    `);
    assert.equal(result.dropped, false);
    const tags = elements(result.fragment).map((el) => el.localName);
    assert.deepEqual(tags, ['div', 'p', 'strong', 'a']);
    const div = elements(result.fragment)[0];
    assert.equal(div.getAttribute('data-id'), 'x');
    assert.equal(div.getAttribute('style'), null);
    assert.equal(div.getAttribute('onclick'), null);
    assert.equal(div.getAttribute('class'), null);
    const anchor = elements(result.fragment).find((el) => el.localName === 'a')!;
    assert.equal(anchor.getAttribute('href'), 'https://ok.test');
    assert.equal(anchor.getAttribute('onclick'), null);
  });

  it('URL 属性拒绝非白名单 scheme 时整体删除该属性', () => {
    const result = clean(
      '<a href="javascript:alert(1)">x</a>' +
      '<img src="data:image/png;base64,AAAA" alt="t">' +
      '<a href="mailto:ok@example.test">m</a>',
    );
    assert.equal(result.dropped, false);
    const [first, image, mail] = elements(result.fragment);
    assert.equal(first.getAttribute('href'), null);
    assert.equal(image.getAttribute('src'), null);
    assert.equal(image.getAttribute('alt'), 't');
    assert.equal(mail.getAttribute('href'), 'mailto:ok@example.test');
  });

  it('注释节点一律不保留', () => {
    const result = clean('<p>a<!-- secret -->b</p>');
    assert.equal(result.dropped, false);
    assert.equal(serialize(result.fragment), '<p>ab</p>');
  });
});

describe('命名空间混淆：svg/math/foreignObject 整棵移除', () => {
  it('移除 svg 及其内部 breakout 到 HTML 的元素', () => {
    const result = clean(`
      <p>before</p>
      <svg>
        <a href="javascript:alert(1)"><text>x</text></a>
        <foreignObject>
          <div xmlns="http://www.w3.org/1999/xhtml">
            <img src=x onerror=alert(1)>
          </div>
        </foreignObject>
      </svg>
      <p>after</p>
    `);
    assert.equal(result.dropped, false);
    const namespaces = new Set(elements(result.fragment).map((el) => el.namespaceURI));
    assert.deepEqual([...namespaces], [HTML_NS]);
    const tags = elements(result.fragment).map((el) => el.localName);
    assert.deepEqual(tags, ['p', 'p']);
    assert.equal(serialize(result.fragment), '<p>before</p><p>after</p>');
  });

  it('foreignObject 内 breakout 的 HTML img 不允许借道存活', () => {
    const tree = parseHTML(
      '<svg><foreignObject><img src="https://x.test/a.png" alt="ok"></foreignObject></svg>',
    );
    const parsedElements = elements(tree);
    assert.ok(parsedElements.some((el) => el.namespaceURI === SVG_NS));
    assert.ok(parsedElements.some((el) => el.namespaceURI === HTML_NS &&
      el.localName === 'img'));
    const result = sanitizePaste(tree, host);
    assert.equal(result.dropped, false);
    assert.equal(elements(result.fragment).length, 0);
  });

  it('移除 math 命名空间切换与 annotation xhtml 嵌套', () => {
    const result = clean(`
      <math>
        <mi>x</mi>
        <annotation-xml encoding="text/html">
          <a href="javascript:alert(1)">click</a>
        </annotation-xml>
      </math>
      <span>safe</span>
    `);
    assert.equal(result.dropped, false);
    const tags = elements(result.fragment);
    assert.equal(tags.length, 1);
    assert.equal(tags[0].namespaceURI, HTML_NS);
    assert.equal(tags[0].localName, 'span');
    assert.equal(SVG_NS !== HTML_NS && MATHML_NS !== HTML_NS, true);
  });

  it('处理伪造命名空间属性与前缀混淆标签', () => {
    const result = clean(`
      <svg:svg xmlns:svg="http://www.w3.org/2000/svg">
        <svg:script>alert(1)</svg:script>
      </svg:svg>
      <DIV TITLE="t">mixed case</DIV>
    `);
    assert.equal(result.dropped, false);
    const tags = elements(result.fragment);
    assert.equal(tags.length, 1);
    assert.equal(tags[0].localName, 'div');
    assert.equal(tags[0].getAttribute('title'), 't');
  });
});
