import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { bookSchema } from '../src/schemas/book-schema.ts';
import { jsonToTree, xmlToTree } from '../src/tree.ts';
import { validate } from '../src/validator.ts';

const validDocument = {
  book: {
    title: '树自动机导论',
    author: { name: '张三', email: 'zhangsan@example.com' },
    paragraph: ['第一段。', '第二段。'],
  },
};

describe('书籍文档模式校验', () => {
  it('合法 JSON 文档通过校验', () => {
    const result = validate(bookSchema, jsonToTree(validDocument));
    assert.equal(result.valid, true);
    assert.deepEqual(result.violations, []);
  });

  it('缺失必要字段：定位 /book/author 并给出精细诊断', () => {
    const document = {
      book: { title: '树自动机导论', author: {}, paragraph: ['第一段。'] },
    };
    const result = validate(bookSchema, jsonToTree(document));
    assert.equal(result.valid, false);
    assert.equal(result.violations.length, 1);
    assert.equal(result.violations[0].path, '/book/author');
    assert.equal(result.violations[0].message, '预期出现[name, email]，实际未找到');
  });

  it('部分缺失：缺少 email', () => {
    const document = {
      book: { title: 'T', author: { name: '张三' }, paragraph: ['段落。'] },
    };
    const result = validate(bookSchema, jsonToTree(document));
    assert.deepEqual(
      result.violations.map((v) => [v.path, v.message]),
      [['/book/author', '预期出现[email]，实际未找到']],
    );
  });

  it('顺序错误：author 出现在 title 之前', () => {
    const document = {
      book: { author: { name: 'N', email: 'e@x.com' }, title: 'T', paragraph: ['p'] },
    };
    const result = validate(bookSchema, jsonToTree(document));
    assert.equal(result.valid, false);
    assert.equal(result.violations[0].path, '/book/author');
    assert.equal(result.violations[0].message, "预期出现[title]，实际出现 'author'");
  });

  it('缺少段落：paragraph 至少出现一次', () => {
    const document = { book: { title: 'T', author: { name: 'N', email: 'e@x.com' } } };
    const result = validate(bookSchema, jsonToTree(document));
    assert.equal(result.violations[0].path, '/book');
    assert.equal(result.violations[0].message, '预期出现[paragraph]，实际未找到');
  });

  it('未定义的标签被报告且不再深入', () => {
    const document = {
      book: { title: 'T', author: { name: 'N', email: 'e@x.com', extra: {} }, paragraph: ['p'] },
    };
    const result = validate(bookSchema, jsonToTree(document));
    const messages = result.violations
      .filter((v) => v.path === '/book/author/extra')
      .map((v) => v.message);
    assert.ok(messages.some((m) => m.includes('此处不允许再出现子节点')));
    assert.ok(messages.some((m) => m.includes("未定义的标签 'extra'")));
  });

  it('同名兄弟节点路径带序号', () => {
    const document = {
      book: {
        title: 'T',
        author: { name: 'N', email: 'e@x.com' },
        paragraph: ['p1', ''],
      },
    };
    const result = validate(bookSchema, jsonToTree(document));
    const emptyParagraph = result.violations.find((v) => v.path === '/book/paragraph[2]');
    assert.ok(emptyParagraph);
    assert.match(emptyParagraph.message, /预期包含文本内容/);
  });

  it('根标签不匹配', () => {
    const result = validate(bookSchema, jsonToTree({ magazine: {} }));
    assert.equal(result.violations[0].message, "根标签不匹配：预期 'book'，实际为 'magazine'");
  });

  it('XML 与 JSON 走同一套校验', () => {
    const xml = `<book>
      <title>T</title>
      <author></author>
      <paragraph>p</paragraph>
    </book>`;
    const result = validate(bookSchema, xmlToTree(xml));
    assert.deepEqual(
      result.violations.map((v) => [v.path, v.message]),
      [['/book/author', '预期出现[name, email]，实际未找到']],
    );
  });

  it('合法 XML 文档通过校验', () => {
    const xml = `<book>
      <title>T</title>
      <author><name>李四</name><email>lisi@example.com</email></author>
      <paragraph>p</paragraph>
    </book>`;
    assert.equal(validate(bookSchema, xmlToTree(xml)).valid, true);
  });
});
