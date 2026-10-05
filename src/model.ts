/**
 * 构建模型：把一个构建描述为「有限输入域 → (缓存键, 产物+日志)」的函数。
 *
 * 输入分三类：
 *  - env              环境变量
 *  - tool             工具版本（编译器、链接器等）
 *  - conditional-read 条件读取：仅当 readWhen 满足时构建才会读取该输入
 */

export type InputKind = "env" | "tool" | "conditional-read";

export type Assignment = Record<string, string>;

export interface InputVar {
  /** 变量名，建议带类别前缀，如 "env:NODE_ENV"、"tool:compiler"、"read:extra.conf" */
  readonly name: string;
  readonly kind: InputKind;
  /** 有限可枚举的取值域 */
  readonly domain: readonly string[];
  /** 条件读取：返回 false 表示该赋值下构建不会读取此输入 */
  readonly readWhen?: (a: Assignment) => boolean;
}

export interface BuildOutput {
  /** 产物的规范化表示（内容寻址的结果、二进制哈希等） */
  readonly artifact: string;
  /** 构建日志行 */
  readonly logs: readonly string[];
}

export interface BuildSpec {
  readonly name: string;
  readonly inputs: readonly InputVar[];
  /** 声明的缓存键：键中编码了哪些输入，由构建系统配置决定 */
  readonly key: (a: Assignment) => string;
  /** 实际构建：给定输入，产生产物与日志 */
  readonly run: (a: Assignment) => BuildOutput;
}

/** 枚举全部输入赋值（笛卡尔积，顺序固定） */
export function enumerateAssignments(inputs: readonly InputVar[]): Assignment[] {
  const out: Assignment[] = [];
  const walk = (i: number, acc: Assignment): void => {
    if (i === inputs.length) {
      out.push({ ...acc });
      return;
    }
    for (const value of inputs[i].domain) {
      acc[inputs[i].name] = value;
      walk(i + 1, acc);
    }
  };
  walk(0, {});
  return out;
}

/** 赋值的规范编码（与变量顺序无关） */
export function encodeAssignment(a: Assignment, inputs: readonly InputVar[]): string {
  return JSON.stringify(inputs.map((input) => a[input.name]));
}
