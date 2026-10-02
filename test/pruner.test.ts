import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { BinaryExpr, FieldRef, FuncCall, Literal } from '../src/expression.ts';
import { Filter, Join, Project, Scan } from '../src/operators.ts';
import { ProjectionPruner } from '../src/pruner.ts';

const ALL_COLUMNS = (() => {
  const cols = Array.from({ length: 20 }, (_, i) => `col${String(i + 1).padStart(2, '0')}`);
  cols[0] = 'id';
  cols[1] = 'name';
  return cols;
})();

describe('ProjectionPruner', () => {
  it('验收边界：20 字段全表，顶层需 id/name，Join 需 id，Scan 被压缩为 [id, name]', () => {
    const leftScan = new Scan('users', ALL_COLUMNS);
    const rightScan = new Scan('orders', ['id', 'user_id', 'amount', 'status']);
    const plan = new Project(
      [
        { name: 'id', expr: new FieldRef('id') },
        { name: 'name', expr: new FieldRef('name') },
      ],
      new Join(leftScan, rightScan, ['id'], ['user_id']),
    );

    assert.equal(leftScan.outputSchema().length, 20);

    ProjectionPruner.prune(plan);

    assert.deepEqual(leftScan.outputSchema(), ['id', 'name']);
    // 右表：上层不需要任何列，但连接键 user_id 必须保留
    assert.deepEqual(rightScan.outputSchema(), ['user_id']);
  });

  it('Project 裁剪掉上层不消费的输出列', () => {
    const scan = new Scan('t', ['a', 'b', 'c']);
    const plan = new Project(
      [
        { name: 'x', expr: new FieldRef('a') },
        { name: 'y', expr: new FieldRef('b') },
      ],
      scan,
    );

    ProjectionPruner.prune(plan, ['x']);

    assert.deepEqual(plan.outputSchema(), ['x']);
    assert.deepEqual(scan.outputSchema(), ['a']);
  });

  it('Filter 谓词引用的字段会被额外向下请求', () => {
    const scan = new Scan('t', ['id', 'age', 'city']);
    const plan = new Project(
      [{ name: 'id', expr: new FieldRef('id') }],
      new Filter(new BinaryExpr('>', new FieldRef('age'), new Literal(18)), scan),
    );

    ProjectionPruner.prune(plan);

    assert.deepEqual(scan.outputSchema(), ['id', 'age']);
  });

  it('表达式中的字段依赖被正确收集（函数调用）', () => {
    const scan = new Scan('t', ['id', 'name', 'secret']);
    const plan = new Project(
      [{ name: 'uname', expr: new FuncCall('upper', [new FieldRef('name')]) }],
      scan,
    );

    ProjectionPruner.prune(plan);

    assert.deepEqual(scan.outputSchema(), ['name']);
  });

  it('Join 把上层需求按左右两侧拆分', () => {
    const left = new Scan('l', ['id', 'la', 'lb']);
    const right = new Scan('r', ['id', 'ra', 'rb']);
    const plan = new Project(
      [
        { name: 'la', expr: new FieldRef('la') },
        { name: 'rb', expr: new FieldRef('rb') },
      ],
      new Join(left, right, ['id'], ['id']),
    );

    ProjectionPruner.prune(plan);

    assert.deepEqual(left.outputSchema(), ['id', 'la']);
    assert.deepEqual(right.outputSchema(), ['id', 'rb']);
  });

  it('缺省顶层需求 = 根算子全部输出列', () => {
    const scan = new Scan('t', ['a', 'b']);
    const plan = new Project(
      [
        { name: 'a', expr: new FieldRef('a') },
        { name: 'b', expr: new FieldRef('b') },
      ],
      scan,
    );

    ProjectionPruner.prune(plan);

    assert.deepEqual(scan.outputSchema(), ['a', 'b']);
  });

  it('上层需求中的未知列被忽略，不会污染 Scan 模式', () => {
    const scan = new Scan('t', ['a', 'b']);
    const plan = new Project([{ name: 'a', expr: new FieldRef('a') }], scan);

    ProjectionPruner.prune(plan, ['a', 'ghost']);

    assert.deepEqual(scan.outputSchema(), ['a']);
  });
});
