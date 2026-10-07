import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import {
  RangeAffinity,
  mapPositionThroughEdit,
  mapRangeThroughEdit,
  mapRangeThroughEdits,
  range,
  type Edit,
} from '../src/index.ts';

describe('mapPositionThroughEdit', () => {
  const edit: Edit = { start: 5, end: 8, text: 'ab' }; // 删除 3 字符插入 2 字符，delta = -1

  it('编辑之前的位置不变', () => {
    assert.equal(mapPositionThroughEdit(4, edit, 'before'), 4);
  });

  it('编辑之后的位置平移', () => {
    assert.equal(mapPositionThroughEdit(10, edit, 'before'), 9);
  });

  it('严格内部的位置失效', () => {
    assert.equal(mapPositionThroughEdit(6, edit, 'before'), undefined);
    assert.equal(mapPositionThroughEdit(7, edit, 'after'), undefined);
  });

  it('非插入编辑边界上的位置贴到替换内容边缘', () => {
    assert.equal(mapPositionThroughEdit(5, edit, 'before'), 5);
    assert.equal(mapPositionThroughEdit(8, edit, 'after'), 7);
  });

  it('边界插入按亲和性映射', () => {
    const insert: Edit = { start: 5, end: 5, text: 'xyz' };
    assert.equal(mapPositionThroughEdit(5, insert, 'before'), 5);
    assert.equal(mapPositionThroughEdit(5, insert, 'after'), 8);
  });
});

describe('mapRangeThroughEdit', () => {
  it('编辑触及范围内部则失效', () => {
    const target = range(5, 10);
    // 删除范围内部的一段
    assert.equal(
      mapRangeThroughEdit(target, { start: 6, end: 8, text: '' }, RangeAffinity.Expand),
      undefined,
    );
    // 替换恰好覆盖整个范围
    assert.equal(
      mapRangeThroughEdit(target, { start: 5, end: 10, text: 'x' }, RangeAffinity.Expand),
      undefined,
    );
    // 从范围起点开始的删除
    assert.equal(
      mapRangeThroughEdit(target, { start: 5, end: 7, text: '' }, RangeAffinity.Expand),
      undefined,
    );
    // 到范围终点为止的删除
    assert.equal(
      mapRangeThroughEdit(target, { start: 8, end: 10, text: '' }, RangeAffinity.Expand),
      undefined,
    );
  });

  it('只接触边界的删除不失效', () => {
    const target = range(5, 10);
    // 删除完全在范围之前，终点贴着范围起点
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 2, end: 5, text: '' }, RangeAffinity.Expand),
      range(2, 7),
    );
    // 删除完全在范围之后，起点贴着范围终点
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 10, end: 12, text: '' }, RangeAffinity.Expand),
      range(5, 10),
    );
  });

  it('范围内部的纯插入使范围扩张', () => {
    assert.deepEqual(
      mapRangeThroughEdit(range(5, 10), { start: 7, end: 7, text: 'ab' }, RangeAffinity.Expand),
      range(5, 12),
    );
  });

  it('边界插入按 Expand 亲和性被吸收', () => {
    const target = range(5, 10);
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 5, end: 5, text: 'ab' }, RangeAffinity.Expand),
      range(5, 12),
    );
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 10, end: 10, text: 'ab' }, RangeAffinity.Expand),
      range(5, 12),
    );
  });

  it('边界插入按 Collapse 亲和性被排除', () => {
    const target = range(5, 10);
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 5, end: 5, text: 'ab' }, RangeAffinity.Collapse),
      range(7, 12),
    );
    assert.deepEqual(
      mapRangeThroughEdit(target, { start: 10, end: 10, text: 'ab' }, RangeAffinity.Collapse),
      range(5, 10),
    );
  });

  it('空范围上的插入：Expand 吸收，Collapse 坍缩到插入之后', () => {
    const cursor = range(5, 5);
    const insert: Edit = { start: 5, end: 5, text: 'ab' };
    assert.deepEqual(mapRangeThroughEdit(cursor, insert, RangeAffinity.Expand), range(5, 7));
    assert.deepEqual(mapRangeThroughEdit(cursor, insert, RangeAffinity.Collapse), range(7, 7));
  });

  it('空范围被删除覆盖时失效', () => {
    assert.equal(
      mapRangeThroughEdit(range(5, 5), { start: 3, end: 8, text: '' }, RangeAffinity.Expand),
      undefined,
    );
  });
});

describe('mapRangeThroughEdits', () => {
  it('顺序折叠多个编辑', () => {
    const edits: Edit[] = [
      { start: 0, end: 0, text: '>> ' }, // 开头插入 3 字符
      { start: 14, end: 14, text: '!' }, // 在映射后范围 [8,13) 之外再插入
    ];
    assert.deepEqual(mapRangeThroughEdits(range(5, 10), edits, RangeAffinity.Expand), range(8, 13));
  });

  it('任一步失效则整体失效', () => {
    const edits: Edit[] = [
      { start: 0, end: 0, text: '>> ' },
      { start: 9, end: 12, text: '' }, // 删除落在映射后范围 [8,13) 内部
    ];
    assert.equal(mapRangeThroughEdits(range(5, 10), edits, RangeAffinity.Expand), undefined);
  });
});
