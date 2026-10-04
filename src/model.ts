export interface FD {
  lhs: number[];
  rhs: number[];
}

export interface Schema {
  attributes: string[];
  fds: FD[];
  decomposition: number[][];
}

export const MAX_ATTRIBUTES = 10;

export class InputError extends Error {}

interface RawFD {
  lhs: string[];
  rhs: string[];
}

interface RawInput {
  attributes: string[];
  fds: Array<RawFD | string>;
  decomposition: string[][];
}

function parseFdString(text: string, attrIndex: Map<string, number>): RawFD {
  const parts = text.split(/->|→/);
  if (parts.length !== 2) {
    throw new InputError(`无法解析函数依赖 "${text}"，期望形如 "A->BC"`);
  }
  const parseSide = (side: string): string[] => {
    const names: string[] = [];
    const tokens = side
      .split(/[,\s]+/)
      .map((token) => token.trim())
      .filter((token) => token.length > 0);
    for (const token of tokens) {
      if (attrIndex.has(token)) {
        names.push(token);
        continue;
      }
      const chars = token.split('');
      if (chars.length > 1 && chars.every((ch) => attrIndex.has(ch))) {
        names.push(...chars);
        continue;
      }
      names.push(token);
    }
    return names;
  };
  return { lhs: parseSide(parts[0]), rhs: parseSide(parts[1]) };
}

function toIndexSet(names: string[], attrIndex: Map<string, number>, context: string): number[] {
  const result: number[] = [];
  const seen = new Set<number>();
  for (const name of names) {
    const idx = attrIndex.get(name);
    if (idx === undefined) {
      throw new InputError(`${context} 引用了未知属性 "${name}"`);
    }
    if (!seen.has(idx)) {
      seen.add(idx);
      result.push(idx);
    }
  }
  result.sort((a, b) => a - b);
  return result;
}

export function normalizeInput(raw: RawInput): Schema {
  if (!raw || typeof raw !== 'object') {
    throw new InputError('输入必须是 JSON 对象');
  }
  const { attributes, fds, decomposition } = raw;
  if (!Array.isArray(attributes) || attributes.length === 0) {
    throw new InputError('attributes 必须是非空数组');
  }
  if (attributes.length > MAX_ATTRIBUTES) {
    throw new InputError(`属性个数 ${attributes.length} 超过上限 ${MAX_ATTRIBUTES}`);
  }
  const attrIndex = new Map<string, number>();
  attributes.forEach((name, idx) => {
    if (typeof name !== 'string' || name.length === 0) {
      throw new InputError(`属性名无效: ${JSON.stringify(name)}`);
    }
    if (attrIndex.has(name)) {
      throw new InputError(`属性 "${name}" 重复`);
    }
    attrIndex.set(name, idx);
  });

  if (!Array.isArray(fds)) {
    throw new InputError('fds 必须是数组');
  }
  const normalizedFds: FD[] = fds.map((entry, i) => {
    const parsed: RawFD = typeof entry === 'string' ? parseFdString(entry, attrIndex) : entry;
    if (!parsed || !Array.isArray(parsed.lhs) || !Array.isArray(parsed.rhs)) {
      throw new InputError(`第 ${i + 1} 个函数依赖格式无效`);
    }
    const lhs = toIndexSet(parsed.lhs, attrIndex, `FD#${i + 1} 左部`);
    const rhs = toIndexSet(parsed.rhs, attrIndex, `FD#${i + 1} 右部`);
    if (lhs.length === 0 || rhs.length === 0) {
      throw new InputError(`FD#${i + 1} 左右部均不能为空`);
    }
    return { lhs, rhs };
  });

  if (!Array.isArray(decomposition) || decomposition.length === 0) {
    throw new InputError('decomposition 必须是非空数组');
  }
  const normalizedDecomp = decomposition.map((member, i) => {
    if (!Array.isArray(member) || member.length === 0) {
      throw new InputError(`分解第 ${i + 1} 个子模式必须是非空数组`);
    }
    return toIndexSet(member, attrIndex, `分解第 ${i + 1} 个子模式`);
  });

  const covered = new Set<number>();
  for (const member of normalizedDecomp) {
    for (const attr of member) {
      covered.add(attr);
    }
  }
  if (covered.size !== attributes.length) {
    const missing = attributes.filter((_, idx) => !covered.has(idx));
    throw new InputError(`分解未覆盖全部属性，缺少: ${missing.join(', ')}`);
  }

  return { attributes, fds: normalizedFds, decomposition: normalizedDecomp };
}

export function formatAttrSet(schema: Schema, attrs: readonly number[]): string {
  if (attrs.length === 0) {
    return '∅';
  }
  return attrs.map((idx) => schema.attributes[idx]).join('');
}

export function formatFd(schema: Schema, fd: FD): string {
  return `${formatAttrSet(schema, fd.lhs)}→${formatAttrSet(schema, fd.rhs)}`;
}
