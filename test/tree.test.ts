import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { jsonToTree, xmlToTree } from '../src/tree.ts';

describe('JSON → 树', () => {
  it('对象属性成为有序子节点，原始值成为文本', () => {
    const tree = jsonToTree({ book: { title: 'T', author: { name: 'N' } } });
    assert.equal(tree.label, 'book');
    assert.deepEqual(tree.children.map((c) => c.label), ['title', 'author']);
    assert.equal(tree.children[0].text, 'T');
    assert.deepEqual(tree.children[1].children.map((c) => c.label), ['name']);
  });

  it('数组展开为同名兄弟节点', () => {
    const tree = jsonToTree({ book: { paragraph: ['a', 'b', 'c'] } });
    assert.deepEqual(tree.children.map((c) => c.label), ['paragraph', 'paragraph', 'paragraph']);
    assert.equal(tree.children[2].text, 'c');
  });

  it('空对象成为无子节点', () => {
    const tree = jsonToTree({ book: { author: {} } });
    assert.equal(tree.children[0].label, 'author');
    assert.equal(tree.children[0].children.length, 0);
  });
});

describe('XML → 树', () => {
  it('解析嵌套元素与文本', () => {
    const tree = xmlToTree('<book><title>T</title><author><name>N</name></author></book>');
    assert.equal(tree.label, 'book');
    assert.deepEqual(tree.children.map((c) => c.label), ['title', 'author']);
    assert.equal(tree.children[1].children[0].text, 'N');
  });

  it('忽略声明、注释与属性', () => {
    const tree = xmlToTree('<?xml version="1.0"?><!-- c --><book lang="zh"><title>T</title></book>');
    assert.equal(tree.label, 'book');
    assert.equal(tree.children[0].text, 'T');
  });

  it('标签不匹配时抛出语法错误', () => {
    assert.throws(() => xmlToTree('<book><title></book>'), SyntaxError);
  });
});
