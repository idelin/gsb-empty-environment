import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  checkSequence,
  choice,
  derive,
  empty,
  label,
  matches,
  nullable,
  opt,
  plus,
  requiredPrefix,
  seq,
  star,
} from '../src/regex.ts';

describe('正规式匹配（Brzozowski 导数）', () => {
  it('顺序：title 后接 1 个或多个 paragraph', () => {
    const pattern = seq(label('title'), plus(label('paragraph')));
    assert.equal(matches(pattern, ['title', 'paragraph']), true);
    assert.equal(matches(pattern, ['title', 'paragraph', 'paragraph']), true);
    assert.equal(matches(pattern, ['title']), false);
    assert.equal(matches(pattern, ['paragraph', 'title']), false);
    assert.equal(matches(pattern, []), false);
  });

  it('选择与可选', () => {
    const pattern = seq(choice(label('a'), label('b')), opt(label('c')));
    assert.equal(matches(pattern, ['a']), true);
    assert.equal(matches(pattern, ['b', 'c']), true);
    assert.equal(matches(pattern, ['c']), false);
    assert.equal(matches(pattern, ['a', 'c', 'c']), false);
  });

  it('克林闭包与空序列', () => {
    assert.equal(matches(star(label('a')), []), true);
    assert.equal(matches(star(label('a')), ['a', 'a']), true);
    assert.equal(matches(empty(), []), true);
    assert.equal(nullable(plus(label('a'))), false);
  });

  it('求导产生剩余式', () => {
    const pattern = seq(label('a'), label('b'));
    const residual = derive(pattern, 'a');
    assert.equal(matches(residual, ['b']), true);
    assert.equal(matches(residual, []), false);
  });
});

describe('序列诊断', () => {
  it('整体缺失：给出完整强制前缀', () => {
    const error = checkSequence(seq(label('name'), label('email')), []);
    assert.deepEqual(error, { kind: 'missing', index: 0, expected: ['name', 'email'] });
  });

  it('部分缺失：给出剩余强制前缀', () => {
    const pattern = seq(label('title'), label('author'), plus(label('paragraph')));
    const error = checkSequence(pattern, ['title']);
    assert.deepEqual(error, { kind: 'missing', index: 1, expected: ['author', 'paragraph'] });
  });

  it('意外标签：给出该位置允许的标签', () => {
    const pattern = seq(label('title'), label('author'));
    const error = checkSequence(pattern, ['author', 'title']);
    assert.deepEqual(error, { kind: 'unexpected', index: 0, expected: ['title'], actual: 'author' });
  });

  it('requiredPrefix 遇到重复即停止', () => {
    const pattern = seq(label('a'), star(label('b')));
    assert.deepEqual(requiredPrefix(pattern), ['a']);
  });
});
