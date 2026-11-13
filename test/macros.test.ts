import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  expandMacros,
  MacroCycleError,
  MacroDepthError,
  MAX_MACRO_DEPTH,
  UndefinedMacroError,
} from '../index.ts';
import { and, cmp, containsMacro, macro, or } from './ast-builders.ts';
import type { ConditionNode } from '../src/types.ts';

test('直接自引用宏：抛出 MacroCycleError 并给出环链', () => {
  const macros: Record<string, ConditionNode> = {};
  macros.loop = macro('loop');

  assert.throws(
    () => expandMacros(macro('loop'), macros),
    (error: unknown) => {
      assert.ok(error instanceof MacroCycleError);
      assert.deepEqual(error.cycle, ['loop', 'loop']);
      return true;
    },
  );
});

test('跨宏循环引用 a -> b -> c -> a：拒绝并报告完整环', () => {
  const macros: Record<string, ConditionNode> = {
    a: macro('b'),
    b: macro('c'),
    c: macro('a'),
  };

  assert.throws(
    () => expandMacros(macro('a'), macros),
    (error: unknown) => {
      assert.ok(error instanceof MacroCycleError);
      assert.deepEqual(error.cycle, ['a', 'b', 'c', 'a']);
      return true;
    },
  );
});

test('非循环的重复引用同一宏可以正常展开', () => {
  const macros: Record<string, ConditionNode> = {
    is_sshd: cmp('proc.name', '=', 'sshd'),
  };
  const expanded = expandMacros(and(macro('is_sshd'), macro('is_sshd')), macros);

  assert.equal(containsMacro(expanded), false);
  assert.equal(expanded.kind, 'and');
  if (expanded.kind === 'and') {
    assert.equal(expanded.operands.length, 2);
    assert.deepEqual(expanded.operands[0], cmp('proc.name', '=', 'sshd'));
  }
});

test('宏展开深度恰好 20 层：通过；第 21 层：抛 MacroDepthError', () => {
  // 构造 m1 -> m2 -> ... -> mN -> 叶子比较
  const buildChain = (length: number): Record<string, ConditionNode> => {
    const macros: Record<string, ConditionNode> = {};
    for (let index = 1; index < length; index += 1) {
      macros[`m${index}`] = macro(`m${index + 1}`);
    }
    macros[`m${length}`] = cmp('proc.name', '=', 'leaf');
    return macros;
  };

  const depth20 = buildChain(MAX_MACRO_DEPTH);
  const expanded = expandMacros(macro('m1'), depth20);
  assert.equal(containsMacro(expanded), false);
  assert.deepEqual(expanded, cmp('proc.name', '=', 'leaf'));

  const depth21 = buildChain(MAX_MACRO_DEPTH + 1);
  assert.throws(
    () => expandMacros(macro('m1'), depth21),
    (error: unknown) => {
      assert.ok(error instanceof MacroDepthError);
      assert.equal(error.depth, MAX_MACRO_DEPTH + 1);
      return true;
    },
  );
});

test('未定义的宏引用：抛 UndefinedMacroError', () => {
  assert.throws(
    () => expandMacros(macro('missing'), {}),
    (error: unknown) => {
      assert.ok(error instanceof UndefinedMacroError);
      assert.match(error.message, /missing/);
      return true;
    },
  );
});

test('展开不修改原始 AST（宏节点保持原样可复用）', () => {
  const original = or(macro('base'), macro('base'));
  const macros: Record<string, ConditionNode> = {
    base: cmp('fd.name', '=', '/etc/passwd'),
  };

  expandMacros(original, macros);

  assert.equal(original.kind, 'or');
  if (original.kind === 'or') {
    assert.deepEqual(original.operands[0], macro('base'));
  }
});

