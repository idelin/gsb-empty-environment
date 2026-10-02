import { test } from 'node:test';
import assert from 'node:assert/strict';
import { bookGrammar } from '../src/book-schema.ts';
import { validateDocument } from '../src/validator.ts';
import { jsonToTree } from '../src/tree.ts';

const validBook = {
  book: {
    title: '树自动机导论',
    author: { name: '张三', email: 'zhangsan@example.com' },
    chapter: [
      { heading: '引言', paragraph: ['第一段', '第二段'] },
      { heading: '正文', paragraph: ['唯一段落'] },
    ],
  },
};

test('合法书籍文档校验通过', () => {
  assert.deepEqual(validateDocument(validBook, bookGrammar), []);
});

test('缺失 author：定位到 /book/author 并给出精细诊断', () => {
  const doc = {
    book: {
      title: '树自动机导论',
      chapter: [{ heading: '引言', paragraph: ['第一段'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].path, '/book/author');
  assert.equal(diagnostics[0].message, '预期出现[name, email]，实际未找到');
});

test('缺失 author.email：定位到 /book/author/email', () => {
  const doc = {
    book: {
      title: 't',
      author: { name: '张三' },
      chapter: [{ heading: 'h', paragraph: ['p'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].path, '/book/author/email');
  assert.equal(diagnostics[0].message, '预期出现[email]，实际未找到');
});

test('子元素顺序错误（email 先于 name）', () => {
  const doc = {
    book: {
      title: 't',
      author: { email: 'a@b.c', name: '张三' },
      chapter: [{ heading: 'h', paragraph: ['p'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].path, '/book/author');
  assert.match(diagnostics[0].message, /预期出现\[name\]/);
  assert.match(diagnostics[0].message, /实际为'email'/);
});

test('缺少必要章节（chapter 至少 1 个）', () => {
  const doc = {
    book: { title: 't', author: { name: 'n', email: 'e' } },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].path, '/book/chapter');
  assert.equal(diagnostics[0].message, '预期出现[heading, paragraph+]，实际未找到');
});

test('出现未定义的多余元素', () => {
  const doc = {
    book: {
      title: 't',
      author: { name: 'n', email: 'e' },
      isbn: '978-0-00',
      chapter: [{ heading: 'h', paragraph: ['p'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.ok(diagnostics.some((d) => d.path === '/book' && d.message.includes(`实际为'isbn'`)));
});

test('chapter 缺失 heading', () => {
  const doc = {
    book: {
      title: 't',
      author: { name: 'n', email: 'e' },
      chapter: [{ paragraph: ['p'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.equal(diagnostics[0].path, '/book/chapter/heading');
  assert.equal(diagnostics[0].message, '预期出现[heading]，实际未找到');
});

test('文本节点不允许包含子元素', () => {
  const doc = {
    book: {
      title: { sub: 'x' },
      author: { name: 'n', email: 'e' },
      chapter: [{ heading: 'h', paragraph: ['p'] }],
    },
  };
  const diagnostics = validateDocument(doc, bookGrammar);
  assert.ok(diagnostics.some((d) => d.path === '/book/title' && d.message.includes('应为文本节点')));
});

test('根元素标签错误', () => {
  const diagnostics = validateDocument({ magazine: {} }, bookGrammar);
  assert.equal(diagnostics.length, 1);
  assert.match(diagnostics[0].message, /文档根元素应为'book'/);
});

test('JSON -> 树映射：数组展开为重复兄弟元素', () => {
  const tree = jsonToTree({ a: [{ b: 1 }, { b: 2 }] }, 'root');
  assert.equal(tree.label, 'root');
  assert.equal(tree.children.length, 2);
  assert.deepEqual(tree.children.map((c) => c.label), ['a', 'a']);
  assert.equal(tree.children[0].children[0].text, '1');
});
