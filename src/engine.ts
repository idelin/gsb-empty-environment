import type {
  InferenceInput,
  InferenceResult,
  Operation,
  OperationType,
  PathResult,
  ProtectionClass,
  Violation,
} from './types.ts';
import { InferenceError } from './types.ts';
import { meetsRequirement } from './classes.ts';
import { strongestRequirement } from './rules.ts';

/** 运行期路径状态：当前类及“决定该类的那一步”的溯源信息。 */
interface PathState {
  path: string;
  protectionClass: ProtectionClass;
  /** 最后一次决定该路径类的操作下标。 */
  decidedByIndex: number;
  decidedById?: string;
  decidedByOp: OperationType;
  /** 弱类由 move 带入时，记录源路径及其最初设类的操作。 */
  moveSource?: string;
  originIndex?: number;
  originId?: string;
  /** 用于最终结果稳定排序：最后作用于该路径的操作序号。 */
  lastTouched: number;
}

function requireExistingSource(
  states: Map<string, PathState>,
  from: string,
  index: number,
  op: OperationType,
): PathState {
  const source = states.get(from);
  if (!source) {
    throw new InferenceError(
      `${op} 的源路径不存在或已被移除: ${from}`,
      index,
      op,
    );
  }
  return source;
}

function buildReason(violation: {
  path: string;
  requiredClass: ProtectionClass;
  actualClass: ProtectionClass;
  violatingOperation: OperationType;
  moveSource?: string;
  originOperationIndex?: number;
}): string {
  const {
    path,
    requiredClass,
    actualClass,
    violatingOperation,
    moveSource,
    originOperationIndex,
  } = violation;

  if (violatingOperation === 'move' && moveSource !== undefined) {
    const originNote =
      originOperationIndex === undefined
        ? ''
        : `，该弱类源自第 ${originOperationIndex} 步对 ${moveSource} 的设类`;
    return (
      `路径 ${path} 要求不弱于 ${requiredClass}，但第 move 步把弱类 ` +
      `${actualClass} 从 ${moveSource} 原样保留并带入规则目录${originNote}`
    );
  }

  return (
    `路径 ${path} 要求不弱于 ${requiredClass}，但 ${violatingOperation} ` +
    `把最终保护类降为 ${actualClass}`
  );
}

/**
 * 按序回放操作序列，推导每个路径的最终保护类，并对照规则报告降级违规。
 */
export function inferProtectionClasses(
  input: InferenceInput,
): InferenceResult {
  const { operations, rules, defaultClass } = input;
  const states = new Map<string, PathState>();

  operations.forEach((operation: Operation, index: number) => {
    const target = operation.path;

    switch (operation.op) {
      case 'create': {
        const protectionClass = operation.protectionClass ?? defaultClass;
        states.set(target, {
          path: target,
          protectionClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'create',
          lastTouched: index,
        });
        break;
      }

      case 'atomicWrite': {
        // 原子写整体替换文件：显式类优先，缺省取应用默认类，
        // 不继承被替换文件的原类，溯源也随之重置。
        const protectionClass = operation.protectionClass ?? defaultClass;
        states.set(target, {
          path: target,
          protectionClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'atomicWrite',
          lastTouched: index,
        });
        break;
      }

      case 'copy': {
        requireExistingSource(states, operation.from, index, 'copy');
        // 副本取应用默认类，不继承源类；其后紧跟的 setAttributes 会就地改类。
        states.set(target, {
          path: target,
          protectionClass: defaultClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'copy',
          lastTouched: index,
        });
        break;
      }

      case 'move': {
        const source = requireExistingSource(
          states,
          operation.from,
          index,
          'move',
        );
        // move 保留原类；记录源路径及其最初设类的操作，便于溯源到根因。
        states.set(target, {
          path: target,
          protectionClass: source.protectionClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'move',
          moveSource: operation.from,
          originIndex: source.originIndex ?? source.decidedByIndex,
          originId: source.originId ?? source.decidedById,
          lastTouched: index,
        });
        if (operation.from !== target) {
          states.delete(operation.from);
        }
        break;
      }

      case 'setAttributes': {
        if (operation.protectionClass === undefined) {
          throw new InferenceError(
            'setAttributes 必须显式指定 protectionClass',
            index,
            'setAttributes',
          );
        }
        const current = states.get(target);
        if (!current) {
          throw new InferenceError(
            `setAttributes 的目标路径不存在: ${target}`,
            index,
            'setAttributes',
          );
        }
        // 就地改类；move 溯源链（若有）继续保留。
        states.set(target, {
          ...current,
          protectionClass: operation.protectionClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'setAttributes',
          lastTouched: index,
        });
        break;
      }

      case 'restore': {
        // 备份恢复一律取应用默认类，丢弃历史类与溯源。
        states.set(target, {
          path: target,
          protectionClass: defaultClass,
          decidedByIndex: index,
          decidedById: operation.id,
          decidedByOp: 'restore',
          lastTouched: index,
        });
        break;
      }

      default: {
        const exhaustiveCheck: never = operation;
        throw new InferenceError(
          `未知操作类型: ${JSON.stringify(exhaustiveCheck)}`,
          index,
        );
      }
    }
  });

  const ordered = [...states.values()].sort(
    (a, b) => a.lastTouched - b.lastTouched,
  );

  const paths: PathResult[] = [];
  const violations: Violation[] = [];

  for (const state of ordered) {
    const requiredClass = strongestRequirement(state.path, rules);
    const compliant =
      requiredClass === undefined ||
      meetsRequirement(state.protectionClass, requiredClass);

    paths.push({
      path: state.path,
      finalClass: state.protectionClass,
      decidingOperationIndex: state.decidedByIndex,
      decidingOperationId: state.decidedById,
      requiredClass,
      compliant,
    });

    if (!compliant && requiredClass !== undefined) {
      const baseViolation = {
        path: state.path,
        requiredClass,
        actualClass: state.protectionClass,
        violatingOperationIndex: state.decidedByIndex,
        violatingOperation: state.decidedByOp,
        violatingOperationId: state.decidedById,
        moveSourcePath: state.moveSource,
        originOperationIndex: state.originIndex,
        originOperationId: state.originId,
      };
      violations.push({
        ...baseViolation,
        reason: buildReason({
          path: state.path,
          requiredClass,
          actualClass: state.protectionClass,
          violatingOperation: state.decidedByOp,
          moveSource: state.moveSource,
          originOperationIndex: state.originIndex,
        }),
      });
    }
  }

  return { paths, violations };
}
