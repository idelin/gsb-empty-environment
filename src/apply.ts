import type { ApplyResult, ChosenPatch, JsonObject, ValidationError } from './types.ts';
import { deepClone, getAtPath, setAtPath, unsetAtPath } from './paths.ts';
import { validateInstance } from './validate.ts';
import type { RootSchema } from './types.ts';

interface UndoEntry {
  path: string;
  existed: boolean;
  previous: unknown;
}

// 原子应用一个已解决的合法计划：
// 1) 不自动改变原始输入（在深拷贝上操作）
// 2) 每个补丁校验前置值；任一失败整体回滚
// 3) 应用后重新验证；仍有错误也整体回滚
export function applyPlan(
  instance: unknown,
  schema: RootSchema,
  plan: { patches: ChosenPatch[] },
): ApplyResult {
  if (plan.patches.some((p) => p.pending)) {
    return {
      ok: false,
      errors: [],
      failedPatch: {
        targetPath: plan.patches.find((p) => p.pending)?.targetPath ?? '',
        reason: '计划包含待人工提供的值（pending），请先通过 providePendingValues 提供具体值后重新搜索/验证',
      },
      rolledBack: false,
    };
  }

  const output = deepClone(instance as JsonObject);
  const undoStack: UndoEntry[] = [];

  const restoreAll = () => {
    while (undoStack.length > 0) {
      const u = undoStack.pop();
      if (!u) break;
      if (u.existed) setAtPath(output, u.path, u.previous);
      else unsetAtPath(output, u.path);
    }
  };

  // 父路径优先，保证嵌套创建顺序
  const ordered = [...plan.patches].sort((a, b) => {
    const da = a.targetPath.split('/').length;
    const db = b.targetPath.split('/').length;
    return da - db || a.targetPath.localeCompare(b.targetPath);
  });

  for (const patch of ordered) {
    const cur = getAtPath(output, patch.targetPath);
    if (patch.op === 'unset') {
      if (!cur.exists) {
        restoreAll();
        return {
          ok: false,
          errors: [],
          failedPatch: { targetPath: patch.targetPath, reason: '前置值不满足：应用 unset 时路径已不存在' },
          rolledBack: true,
        };
      }
      undoStack.push({ path: patch.targetPath, existed: true, previous: cur.value });
      unsetAtPath(output, patch.targetPath);
    } else {
      undoStack.push({
        path: patch.targetPath,
        existed: cur.exists,
        previous: cur.value,
      });
      try {
        setAtPath(output, patch.targetPath, patch.value);
      } catch (e) {
        restoreAll();
        return {
          ok: false,
          errors: [],
          failedPatch: { targetPath: patch.targetPath, reason: (e as Error).message },
          rolledBack: true,
        };
      }
    }
  }

  // 应用后重新验证（不假设搜索阶段结论永远成立）
  const recheck = validateInstance(output, schema);
  if (!recheck.valid) {
    restoreAll();
    return {
      ok: false,
      errors: recheck.errors,
      failedPatch: {
        targetPath: ordered[ordered.length - 1]?.targetPath ?? '',
        reason: '整体应用后重新验证仍不合法，已回滚全部补丁',
      },
      rolledBack: true,
    };
  }

  return { ok: true, output, errors: [], rolledBack: false };
}

// 人工为 pending 路径提供值：写入拷贝并重新验证；不合法则返回错误（输入不变）
export function providePendingValues(
  instance: unknown,
  schema: RootSchema,
  values: Record<string, unknown>,
): ApplyResult {
  const output = deepClone(instance as JsonObject);
  for (const [ptr, value] of Object.entries(values)) {
    try {
      setAtPath(output, ptr, value);
    } catch (e) {
      return {
        ok: false,
        errors: [],
        failedPatch: { targetPath: ptr, reason: (e as Error).message },
        rolledBack: true,
      };
    }
  }
  const recheck = validateInstance(output, schema);
  if (!recheck.valid) {
    return { ok: false, errors: recheck.errors, rolledBack: true };
  }
  return { ok: true, output, errors: [], rolledBack: false };
}
