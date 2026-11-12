import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { sanitizePaste } from '../src/sanitizer.ts';
import type { MiniNode } from '../src/types.ts';
import { createTestDomHost, parseHTML, serialize } from './dom.ts';

const host = createTestDomHost();

function run(html: string) {
  return sanitizePaste(parseHTML(html), host);
}

describe('序列化-再解析往返稳定才放行', () => {
  const stableSamples = [
    '<p>plain text</p>',
    '<div><p>nested</p><ul><li>a</li><li>b</li></ul></div>',
    '<a href="https://example.test/path?q=1#h">link</a>',
    '<img src="https://cdn.example.test/a.png" alt="a &amp; b" width="100">',
    '<blockquote cite="https://src.test/q"><p>quote</p></blockquote>',
    '<table><thead><tr><th>h</th></tr></thead>' +
      '<tbody><tr><td colspan="2">cell</td></tr></tbody></table>',
    '<pre><code>const x = 1 &lt; 2;</code></pre>',
    '<span data-track="a&amp;b">entities &copy; 2026</span>',
    '<time datetime="2026-11-12">today</time>',
  ];

  for (const sample of stableSamples) {
    it(`稳定往返: ${sample.slice(0, 48)}`, () => {
      const result = run(sample);
      assert.equal(result.dropped, false, result.reason);
      const again = host.parseHTML(host.serialize(result.fragment));
      assert.equal(serialize(again), serialize(result.fragment));
    });
  }

  it('表格 foster parenting 导致结构漂移时整段丢弃', () => {
    // 直接构造一棵违反表格内容模型的树：把块级元素挂到 table 下。
    // 真实浏览器里这类树可能来自编辑器 DOM 操作或不同解析管线的差异；
    // 序列化后再解析时 HTML5 解析器会把它 foster 到 table 外，
    // 与消毒树结构不一致，因此必须整段丢弃。
    const parsed = parseHTML('<table><tbody><tr><td>x</td></tr></tbody></table>');
    const table = parsed.childNodes[0];
    const stray = host.createElement('div');
    host.appendChild(stray, host.createTextNode('foster me'));
    host.appendChild(table, stray);

    const serialized = serialize(parsed);
    assert.notEqual(
      serialize(parseHTML(serialized)),
      serialized,
      '该结构序列化-再解析应当产生不同的树',
    );

    const result = sanitizePaste(parsed, host);
    assert.equal(result.dropped, true);
    assert.equal(result.reason, 'round-trip-mismatch');
    assert.equal(result.fragment.childNodes.length, 0);
  });

  it('解析器自身对 foster 样例是幂等的（对照组）', () => {
    const html = '<div>x</div><table><tbody><tr><td>y</td></tr></tbody></table>';
    const once = serialize(parseHTML(html));
    assert.equal(serialize(parseHTML(once)), once);
    const result = sanitizePaste(parseHTML(html), host);
    assert.equal(result.dropped, false);
  });

  it('合法表格嵌套不触发丢弃', () => {
    const result = run(
      '<table><tbody><tr><td><p>ok</p></td></tr></tbody></table>',
    );
    assert.equal(result.dropped, false);
  });

  it('svg breakout mXSS 载荷消毒后即使被剥离也必须通过往返校验', () => {
    const payloads = [
      '<svg></p><style><a id="</style><img src=x onerror=alert(1)>"></a>',
      '<math><mi//xlink:href="data:x,<script>alert(1)</script>">',
      '<svg><foreignObject><p>break</p></foreignObject></svg>',
      '<p><svg><table><style><img src=x onerror=alert(1)></style></table></svg></p>',
      '<form><math><mtext></form><form><mglyph><style></math><img src=x onerror=alert(1)>',
    ];
    for (const payload of payloads) {
      const result = run(payload);
      if (!result.dropped) {
        const again = host.parseHTML(host.serialize(result.fragment));
        assert.equal(
          serialize(again),
          serialize(result.fragment),
          `消毒结果必须往返一致: ${payload}`,
        );
      }
    }
  });

  it('URL 中的 tab/LF 归一化后仍能稳定往返', () => {
    const result = run('<a href="https://example.test/a\tb?x=1\n">link</a>');
    assert.equal(result.dropped, false);
    assert.equal(
      serialize(result.fragment),
      '<a href="https://example.test/ab?x=1">link</a>',
    );
  });

  it('输入元素节点（而非片段）时同样工作且不修改原树', () => {
    const original = parseHTML('<div><p>x</p></div>');
    const root = original.childNodes[0];
    const snapshot = serialize(original);
    const result = sanitizePaste(root, host);
    assert.equal(result.dropped, false);
    assert.equal(serialize(original), snapshot);
    assert.equal(serialize(result.fragment), '<div><p>x</p></div>');
  });

  it('实体与非 ASCII 文本往返一致', () => {
    const html = '<p title="a &quot; b">&copy; &nbsp; 中文 &lt;tag&gt;</p>';
    const result = run(html);
    assert.equal(result.dropped, false);
    const again = host.parseHTML(host.serialize(result.fragment));
    assert.equal(serialize(again), serialize(result.fragment));
  });
});

describe('解析器命名空间语义自检', () => {
  it('foreignObject 后内部元素回到 HTML 命名空间', () => {
    const tree = parseHTML(
      '<svg><foreignObject><div><img src="x"></div></foreignObject></svg>',
    );
    const walk = (node: MiniNode, out: string[] = []): string[] => {
      if (host.isElement(node)) {
        out.push(`${node.namespaceURI === 'http://www.w3.org/2000/svg' ? 'svg' : 'html'}:${node.localName}`);
      }
      for (const child of node.childNodes) {
        walk(child, out);
      }
      return out;
    };
    assert.deepEqual(walk(tree), [
      'svg:svg',
      'svg:foreignobject',
      'html:div',
      'html:img',
    ]);
  });

  it('breakout 标签把后续插桩点切回 HTML', () => {
    const tree = parseHTML('<svg><a><font><p>x</p></font></a></svg>');
    const namespaces = new Set<string>();
    const walk = (node: MiniNode): void => {
      if (host.isElement(node)) {
        namespaces.add(node.namespaceURI);
      }
      for (const child of node.childNodes) {
        walk(child);
      }
    };
    walk(tree);
    assert.ok(namespaces.has('http://www.w3.org/2000/svg'));
    assert.ok(namespaces.has('http://www.w3.org/1999/xhtml'));
  });
});
