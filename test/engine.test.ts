import { describe, it } from 'node:test';
import assert from 'node:assert/strict';

import { render, compile } from '../src/engine.ts';
import { UnsafeRawOutputError, UnsafeUrlError, escape } from '../src/escaper.ts';

/**
 * 六类注入载荷，分别在六种上下文（正文/属性/脚本/URL/注释/样式）
 * 中验证不能逃逸上下文。
 */
const PAYLOADS = {
  tagInjection: '<img src=x onerror=alert(1)>',
  attributeBreakout: '" onmouseover="alert(1)//',
  scriptStringBreakout: '"; alert(document.cookie); //',
  scriptTagBreakout: '</script><script>alert(1)</script>',
  javascriptUrl: 'javascript:alert(document.domain)',
  commentBreakout: '--><img src=x onerror=alert(1)>',
} as const;

describe('上下文自动转义：六类注入', () => {
  it('1. 元素正文：标签注入被实体化', () => {
    const output = render('<div>{{ x }}</div>', { x: PAYLOADS.tagInjection });
    assert.ok(!output.includes('<img'));
    assert.equal(
      output,
      '<div>&lt;img src=x onerror=alert(1)&gt;</div>',
    );
  });

  it('2. 属性值：引号闭合注入被阻止', () => {
    const output = render('<input value="{{ x }}">', {
      x: PAYLOADS.attributeBreakout,
    });
    assert.ok(!output.includes('onmouseover="alert'));
    assert.equal(output.match(/&quot;/g)?.length, 2);
    assert.equal(
      output,
      '<input value="&quot;&#32;onmouseover=&quot;alert(1)//">',
    );
  });

  it('3. 脚本字符串：变量输出为 JSON 字面量且无法闭合字符串', () => {
    const output = render('<script>var a = {{ x }};</script>', {
      x: PAYLOADS.scriptStringBreakout,
    });
    assert.ok(output.includes('var a = "'));
    assert.match(output, /\\"; alert/);
    assert.equal(
      output,
      '<script>var a = "\\"; alert(document.cookie); //";;</script>',
    );
    const literal = output.match(/var a = (.*);;<\/script>/)?.[1] ?? '';
    assert.equal(JSON.parse(literal), PAYLOADS.scriptStringBreakout);
  });

  it('4. 脚本区：</script> 标签注入被 unicode 转义', () => {
    const output = render('<script>var a = {{ x }};</script>', {
      x: PAYLOADS.scriptTagBreakout,
    });
    assert.ok(!output.includes('</script><script>'));
    assert.ok(output.includes('\\u003c/script\\u003e'));
  });

  it('5. URL 属性：javascript: 协议直接拒绝', () => {
    assert.throws(
      () => render('<a href="{{ x }}">go</a>', { x: PAYLOADS.javascriptUrl }),
      UnsafeUrlError,
    );
  });

  it('6. 注释：注释闭合注入被实体化', () => {
    const output = render('<!-- {{ x }} -->', {
      x: PAYLOADS.commentBreakout,
    });
    assert.ok(!output.includes('-->'));
    assert.ok(!output.includes('<img'));
  });

  it('7. 样式：CSS 注入字符被十六进制转义', () => {
    const output = render('<style>body { color: {{ x }}; }</style>', {
      x: 'red } body { background: url(x)',
    });
    assert.ok(!output.includes('} body'));
    assert.match(output, /\\7d /);
  });
});

describe('脚本上下文拒绝未编码原始输出', () => {
  it('{{{ }}} 在 <script> 中抛出 UnsafeRawOutputError', () => {
    assert.throws(
      () => render('<script>var a = {{{ x }}};</script>', { x: '1;alert(1)' }),
      UnsafeRawOutputError,
    );
  });

  it('on* 事件属性中的 {{{ }}} 同样被拒绝', () => {
    assert.throws(
      () => render('<button onclick="{{{ x }}}">ok</button>', {
        x: 'alert(1)',
      }),
      UnsafeRawOutputError,
    );
  });

  it('普通 {{ }} 在事件属性中输出 JSON 字面量并经属性编码', () => {
    const output = render('<button onclick="go({{ x }})">ok</button>', {
      x: 'a"b',
    });
    assert.equal(
      output,
      '<button onclick="go(&quot;a\\&quot;b&quot;)">ok</button>',
    );
  });
});

describe('URL 属性专用编码', () => {
  it('合法 URL 正常渲染，空白被百分号编码', () => {
    const output = render('<a href="{{ x }}">link</a>', {
      x: 'https://example.com/a b',
    });
    assert.equal(output, '<a href="https://example.com/a%20b">link</a>');
  });

  it('混淆协议绕过被拦截', () => {
    assert.throws(
      () => render('<a href="{{ x }}">x</a>', {
        x: 'java\tscript:alert(1)',
      }),
      UnsafeUrlError,
    );
    assert.throws(
      () => render('<a href="{{ x }}">x</a>', {
        x: '&#106;avascript:alert(1)',
      }),
      UnsafeUrlError,
    );
  });

  it('src/action 等 URL 属性同样适用', () => {
    assert.throws(
      () => render('<form action="{{ x }}"></form>', {
        x: 'vbscript:msgbox(1)',
      }),
      UnsafeUrlError,
    );
  });
});

describe('双重编码幂等', () => {
  const contexts = ['html', 'attr', 'url', 'comment', 'css', 'js'] as const;
  const sample = '<b title="x">& \' -- </b>';

  for (const context of contexts) {
    it(`escape 后再 escape 在 ${context} 上下文保持原义`, () => {
      const once = escape(sample, context);
      const twice = escape(once, context);
      assert.equal(twice.value, once.value);
    });
  }

  it('html/attr/comment 的裸字符串编码器本身实体感知、幂等', () => {
    const htmlOnce = escape(sample, 'html').value;
    assert.notEqual(htmlOnce, sample);
    assert.equal(escape(htmlOnce, 'html').value, htmlOnce);

    const attrOnce = escape(sample, 'attr').value;
    assert.equal(escape(attrOnce, 'attr').value, attrOnce);

    const commentOnce = escape(sample, 'comment').value;
    assert.equal(escape(commentOnce, 'comment').value, commentOnce);
  });

  it('引擎渲染已转义的 SafeString 不会二次编码', () => {
    const preEscaped = escape('<b>hi</b>', 'html');
    const output = render('<div>{{ x }}</div>', { x: preEscaped });
    assert.equal(output, '<div>&lt;b&gt;hi&lt;/b&gt;</div>');
  });
});

describe('引擎杂项行为', () => {
  it('{{{ }}} 在正文与普通属性中允许原始输出', () => {
    const output = render('<div title="{{{ t }}">{{{ h }}}</div>', {
      h: '<b>bold</b>',
      t: 'raw',
    });
    assert.equal(output, '<div title="raw"><b>bold</b></div>');
  });

  it('支持点路径与缺失值', () => {
    assert.equal(
      render('{{ a.b }}[{{ missing.path }}]', { a: { b: 'ok' } }),
      'ok[]',
    );
  });

  it('compile 为脚本/样式/注释中的插值标注正确上下文', () => {
    const nodes = compile(
      '<script>{{ a }}</script><style>{{ b }}</style><!-- {{ c }} --><p>{{ d }}</p>',
    );
    const contexts = nodes
      .filter((node) => node.kind === 'interpolation')
      .map((node) => node.context);
    assert.deepEqual(contexts, ['js', 'css', 'comment', 'html']);
  });

  it('安全边界探测：各上下文均无法逃逸', () => {
    assert.equal(
      render('<p>{{ x }}</p>', { x: '</p><script>alert(1)</script>' }),
      '<p>&lt;/p&gt;&lt;script&gt;alert(1)&lt;/script&gt;</p>',
    );

    const singleQuoted = render("<p title='{{ x }}'>", {
      x: "' onclick='alert(1)",
    });
    assert.ok(!singleQuoted.includes("' onclick"));

    const backtick = render('<p t="{{ x }}">', {
      x: '` onerror=alert(1)`',
    });
    assert.ok(!backtick.includes('`'));

    const objectInScript = render('<script>var d={{ x }}</script>', {
      x: { a: '</script><img src=x onerror=alert(1)>' },
    });
    assert.ok(!objectInScript.includes('</script><img'));
    assert.ok(objectInScript.includes('\\u003c/script'));
  });
});
