/**
 * 子节点序列正规式（hedge regular expression）。
 *
 * 树自动机中，每个节点的子节点序列是否合法由一个正规式刻画。
 * 这里采用 Brzozowski 导数（derivative）实现匹配：
 * 每消费一个子节点标签就对正规式求导，最终剩余式可空（nullable）即接受。
 * 求导后的"剩余式"同时用于生成精细诊断（还缺哪些标签）。
 */

export type Regex =
  | { kind: 'empty' } // ε：空序列
  | { kind: 'never' } // ∅：不匹配任何序列
  | { kind: 'label'; name: string } // 单个标签
  | { kind: 'seq'; left: Regex; right: Regex } // 顺序连接
  | { kind: 'choice'; left: Regex; right: Regex } // 选择
  | { kind: 'star'; inner: Regex }; // 克林闭包（0 次或多次）

export const EMPTY: Regex = { kind: 'empty' };
export const NEVER: Regex = { kind: 'never' };

export function empty(): Regex {
  return EMPTY;
}

export function never(): Regex {
  return NEVER;
}

export function label(name: string): Regex {
  return { kind: 'label', name };
}

export function seq(...parts: Regex[]): Regex {
  return parts.reduceRight<Regex>((acc, part) => seq2(part, acc), EMPTY);
}

function seq2(left: Regex, right: Regex): Regex {
  if (left.kind === 'never' || right.kind === 'never') return NEVER;
  if (left.kind === 'empty') return right;
  if (right.kind === 'empty') return left;
  return { kind: 'seq', left, right };
}

export function choice(...alts: Regex[]): Regex {
  return alts.reduce<Regex>((acc, alt) => choice2(acc, alt), NEVER);
}

function choice2(left: Regex, right: Regex): Regex {
  if (left.kind === 'never') return right;
  if (right.kind === 'never') return left;
  return { kind: 'choice', left, right };
}

export function star(inner: Regex): Regex {
  if (inner.kind === 'never' || inner.kind === 'empty') return EMPTY;
  return { kind: 'star', inner };
}

/** 1 次或多次：r+ */
export function plus(inner: Regex): Regex {
  return seq2(inner, star(inner));
}

/** 0 次或 1 次：r? */
export function opt(inner: Regex): Regex {
  return choice2(inner, EMPTY);
}

/** 可空性：该正规式是否接受空序列 */
export function nullable(regex: Regex): boolean {
  switch (regex.kind) {
    case 'empty':
      return true;
    case 'never':
    case 'label':
      return false;
    case 'seq':
      return nullable(regex.left) && nullable(regex.right);
    case 'choice':
      return nullable(regex.left) || nullable(regex.right);
    case 'star':
      return true;
  }
}

/** Brzozowski 导数：消费一个标签后的剩余正规式 */
export function derive(regex: Regex, name: string): Regex {
  switch (regex.kind) {
    case 'empty':
    case 'never':
      return NEVER;
    case 'label':
      return regex.name === name ? EMPTY : NEVER;
    case 'seq': {
      const head = seq2(derive(regex.left, name), regex.right);
      return nullable(regex.left) ? choice2(head, derive(regex.right, name)) : head;
    }
    case 'choice':
      return choice2(derive(regex.left, name), derive(regex.right, name));
    case 'star':
      return seq2(derive(regex.inner, name), regex);
  }
}

/** 完整匹配：标签序列是否被正规式接受 */
export function matches(regex: Regex, labels: string[]): boolean {
  let residual = regex;
  for (const name of labels) {
    residual = derive(residual, name);
    if (residual.kind === 'never') return false;
  }
  return nullable(residual);
}

/** 下一步允许出现的标签集合（first 集） */
export function firstLabels(regex: Regex): string[] {
  const out = new Set<string>();
  collectFirst(regex, out);
  return [...out];
}

function collectFirst(regex: Regex, out: Set<string>): void {
  switch (regex.kind) {
    case 'label':
      out.add(regex.name);
      return;
    case 'seq':
      collectFirst(regex.left, out);
      if (nullable(regex.left)) collectFirst(regex.right, out);
      return;
    case 'choice':
      collectFirst(regex.left, out);
      collectFirst(regex.right, out);
      return;
    case 'star':
      collectFirst(regex.inner, out);
      return;
    case 'empty':
    case 'never':
      return;
  }
}

/**
 * 必须按序出现的标签前缀。
 * 例如 seq(label('name'), label('email')) → ['name', 'email']；
 * 遇到可选/重复/选择即停止（之后的内容不是强制的）。
 */
export function requiredPrefix(regex: Regex): string[] {
  switch (regex.kind) {
    case 'label':
      return [regex.name];
    case 'seq': {
      const head = requiredPrefix(regex.left);
      return isFixedSequence(regex.left)
        ? [...head, ...requiredPrefix(regex.right)]
        : head;
    }
    case 'choice':
    case 'star':
    case 'empty':
    case 'never':
      return [];
  }
}

/** 是否为不含选择的固定标签序列（此时其后的强制前缀也可确定） */
function isFixedSequence(regex: Regex): boolean {
  switch (regex.kind) {
    case 'label':
    case 'empty':
      return true;
    case 'seq':
      return isFixedSequence(regex.left) && isFixedSequence(regex.right);
    case 'choice':
    case 'star':
    case 'never':
      return false;
  }
}

export type SequenceError =
  | { kind: 'missing'; index: number; expected: string[] }
  | { kind: 'unexpected'; index: number; expected: string[]; actual: string };

/**
 * 校验标签序列，失败时返回精细诊断：
 * - missing：序列提前结束，expected 为仍必须出现的标签（按序）；
 * - unexpected：在 index 处出现非法标签，expected 为该位置允许的标签。
 */
export function checkSequence(regex: Regex, labels: string[]): SequenceError | null {
  let residual = regex;
  for (let index = 0; index < labels.length; index += 1) {
    const next = derive(residual, labels[index]);
    if (next.kind === 'never') {
      return {
        kind: 'unexpected',
        index,
        expected: firstLabels(residual),
        actual: labels[index],
      };
    }
    residual = next;
  }
  if (!nullable(residual)) {
    return { kind: 'missing', index: labels.length, expected: expectedLabels(residual) };
  }
  return null;
}

/** 诊断展示用的期望标签列表：优先给出强制前缀，否则给出 first 集 */
function expectedLabels(regex: Regex): string[] {
  const required = requiredPrefix(regex);
  return required.length > 0 ? required : firstLabels(regex);
}
