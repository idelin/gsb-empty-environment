import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import {
  decodeEntities,
  encodeAttr,
  encodeComment,
  encodeCss,
  encodeHtml,
  encodeJs,
  encodeUrl,
  escape,
  SafeString,
  UnsafeUrlError,
} from '../src/escaper.ts';

describe('encodeHtml 元素正文', () => {
  it('转义 & < >', () => {
    assert.equal(
      encodeHtml('<a href="x">&amp;</a>'),
      '&lt;a href="x"&gt;&amp;&lt;/a&gt;',
    );
  });

  it('实体感知：已存在的实体不被二次编码', () => {
    assert.equal(encodeHtml('&lt; &amp; &#60; &#x3C;'), '&lt; &amp; &#60; &#x3C;');
  });

  it('裸 & 仍被编码', () => {
    assert.equal(encodeHtml('a & b'), 'a &amp; b');
  });
});

describe('encodeAttr 属性值', () => {
  it('转义引号、反引号与空白，阻止闭合属性', () => {
    const output = encodeAttr('" onclick="alert(1)" x="');
    assert.ok(!output.includes('"'));
    assert.match(output, /&quot;/);
  });

  it('换行与制表符被实体化，防止属性换行注入', () => {
    const output = encodeAttr('a\nb\tc\rd');
    assert.equal(output, 'a&#10;b&#9;c&#13;d');
  });

  it('对已转义内容幂等', () => {
    const once = encodeAttr(`"&'<>`);
    assert.equal(encodeAttr(once), once);
  });
});

describe('encodeJs 脚本字符串', () => {
  it('输出自带引号的 JSON 字符串字面量', () => {
    assert.equal(encodeJs('hello'), '"hello"');
  });

  it('引号与反斜杠被 JSON 转义，无法闭合字符串', () => {
    const output = encodeJs('"; alert(1); //') as string;
    assert.match(output, /\\"/);
    assert.equal(output, JSON.stringify('"; alert(1); //'));
    assert.equal(JSON.parse(output), '"; alert(1); //');
  });

  it('U+2028/U+2029 与 </script> 被转义', () => {
    const output = encodeJs('</script><script>alert(1)</script>\u2028') as string;
    assert.ok(!output.includes('</script>'));
    assert.ok(!output.includes('\u2028'));
    assert.match(output, /\\u2028/);
  });

  it('对象与数组编码为 JSON 字面量', () => {
    assert.equal(encodeJs({ a: 1, b: 'x' }), '{"a":1,"b":"x"}');
    assert.equal(encodeJs([1, '2']), '[1,"2"]');
    assert.equal(encodeJs(null), 'null');
    assert.equal(encodeJs(undefined), 'undefined');
  });
});

describe('encodeUrl 地址', () => {
  it('允许 http/https/mailto/tel 与相对地址', () => {
    assert.equal(encodeUrl('https://example.com/a b'), 'https://example.com/a%20b');
    assert.equal(encodeUrl('/path?x=1&y=2'), '/path?x=1&y=2');
    assert.equal(encodeUrl('mailto:a@b.com'), 'mailto:a@b.com');
  });

  it('拒绝 javascript:/vbscript:/data: 协议', () => {
    assert.throws(() => encodeUrl('javascript:alert(1)'), UnsafeUrlError);
    assert.throws(() => encodeUrl('vbscript:msgbox(1)'), UnsafeUrlError);
    assert.throws(() => encodeUrl('data:text/html,<script>alert(1)</script>'), UnsafeUrlError);
  });

  it('拦截控制字符与实体混淆的协议绕过', () => {
    assert.throws(() => encodeUrl('java\nscript:alert(1)'), UnsafeUrlError);
    assert.throws(() => encodeUrl('&#106;avascript:alert(1)'), UnsafeUrlError);
    assert.throws(() => encodeUrl('  JavaScript:alert(1)'), UnsafeUrlError);
  });

  it('引号被编码，不能逃逸属性', () => {
    const output = encodeUrl('https://x/" onmouseover="alert(1)');
    assert.ok(!output.includes('"'));
  });
});

describe('encodeComment 注释', () => {
  it('连字符与尖括号被实体化，无法闭合注释', () => {
    const output = encodeComment('--><img src=x onerror=alert(1)>');
    assert.ok(!output.includes('-->'));
    assert.ok(!output.includes('>'));
  });

  it('对已转义内容幂等', () => {
    const once = encodeComment('a -- b & c');
    assert.equal(encodeComment(once), once);
  });
});

describe('encodeCss 样式', () => {
  it('危险字符输出为十六进制转义', () => {
    const output = encodeCss('red; } body { background: url(javascript:alert(1))');
    assert.ok(!output.includes('}'));
    assert.ok(!output.includes('{'));
    assert.ok(!output.includes('javascript:'));
    assert.match(output, /\\7d /);
    assert.match(output, /\\3a /);
  });

  it('表达式注入字符被转义', () => {
    const output = encodeCss('expression(alert(1))');
    assert.ok(!output.includes('('));
    assert.ok(!output.includes(')'));
  });
});

describe('escape 统一入口与 SafeString', () => {
  it('返回 SafeString，重复 escape 不二次编码', () => {
    const once = escape('<b>&</b>', 'html');
    assert.ok(once instanceof SafeString);
    assert.equal(escape(once, 'html'), once);
    assert.equal(escape(once, 'html').value, once.value);
  });

  it('null/undefined 渲染为空字符串', () => {
    assert.equal(escape(undefined, 'html').value, '');
    assert.equal(escape(null, 'attr').value, '');
  });

  it('decodeEntities 解码命名与数值实体', () => {
    assert.equal(decodeEntities('&amp;&lt;&#65;&#x42;'), '&<AB');
  });
});
