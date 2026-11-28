import { XmlSecurityError } from './errors.ts';
import { readName } from './xml-lex.ts';
import type { GeneralEntity } from './dtd.ts';

export interface EntityQuotas {
  /** 整个文档中由引用（含预定义/字符引用）产出的字符总数上限。 */
  maxTotalExpandedChars: number;
  /** 单次实体引用相对自身原始值长度的最大展开倍数。 */
  maxExpansionMultiplier: number;
  /** 实体引用最大嵌套深度（根引用深度为 1）。 */
  maxExpansionDepth: number;
}

export const DEFAULT_ENTITY_QUOTAS: EntityQuotas = {
  maxTotalExpandedChars: 1_000_000,
  maxExpansionMultiplier: 100,
  maxExpansionDepth: 10,
};

const PREDEFINED_ENTITIES: ReadonlyMap<string, string> = new Map([
  ['lt', '<'],
  ['gt', '>'],
  ['amp', '&'],
  ['apos', "'"],
  ['quot', '"'],
]);

/**
 * 以内部通用实体的“值内引用关系”为有向边做 DFS 环检测。
 * 解析期一次性完成，循环实体在任何展开发生前即被拒绝。
 */
export function detectEntityCycles(entities: ReadonlyMap<string, GeneralEntity>): void {
  const state = new Map<string, 0 | 1 | 2>(); // 0=未访问 1=在栈上 2=已完成

  const visit = (name: string, path: string[]): void => {
    const mark = state.get(name) ?? 0;
    if (mark === 1) {
      throw new XmlSecurityError(
        'ENTITY_RECURSION',
        `entity reference cycle detected: ${path.concat(name).join(' -> ')}`,
      );
    }
    if (mark === 2) {
      return;
    }
    state.set(name, 1);
    const entity = entities.get(name);
    if (entity !== undefined) {
      for (const referenced of collectReferencedNames(entity.value)) {
        if (entities.has(referenced)) {
          visit(referenced, path.concat(name));
        }
      }
    }
    state.set(name, 2);
  };

  for (const name of entities.keys()) {
    visit(name, []);
  }
}

function collectReferencedNames(value: string): string[] {
  const names: string[] = [];
  for (let i = 0; i < value.length; i++) {
    if (value[i] !== '&') {
      continue;
    }
    if (value[i + 1] === '#') {
      i++;
      continue;
    }
    const parsed = readName(value, i + 1);
    if (parsed.name !== '' && value[parsed.next] === ';') {
      names.push(parsed.name);
      i = parsed.next;
    }
  }
  return names;
}

interface ExpansionFrame {
  /** 该帧允许产出的字符上限。 */
  cap: number;
  /** 该帧已产出字符数。 */
  emitted: number;
}

/**
 * 有界实体展开验证器：
 * - 帧栈模型，根帧预算即文档总配额；每个实体帧预算为 rawLen * 倍数；
 * - 每段字面量在写入前先做帧预算与全局预算检查，绝不“先展开再测长”；
 * - 仅累计引用产出字符，普通文本不占用实体配额。
 */
export class SafeEntityExpander {
  private readonly entities: ReadonlyMap<string, GeneralEntity>;
  private readonly quotas: EntityQuotas;
  private readonly frames: ExpansionFrame[] = [];
  private totalReferenceChars = 0;
  private readonly expansionWeight = new Map<string, number>();
  private readonly leafLengthCache = new Map<string, number>();

  constructor(entities: ReadonlyMap<string, GeneralEntity>, quotas: EntityQuotas = DEFAULT_ENTITY_QUOTAS) {
    this.entities = entities;
    this.quotas = quotas;
  }

  /**
   * 递归估算实体完全展开后的字符权重。
   * 以总配额为封顶，避免在合法小图上产生大整数；环已在解析期被 DFS 拦截，
   * 因此引用关系是 DAG，可按实体名安全记忆化，共享子图只计算一次。
   */
  private computeWeight(name: string): number {
    if (this.expansionWeight.has(name)) {
      const cached = this.expansionWeight.get(name) as number;
      return cached;
    }
    const entity = this.entities.get(name);
    if (entity === undefined) {
      return 1;
    }
    let weight = 0;
    let i = 0;
    while (i < entity.value.length) {
      const ch = entity.value[i] as string;
      if (ch !== '&') {
        weight += 1;
        i += 1;
        continue;
      }
      if (entity.value[i + 1] === '#') {
        const end = entity.value.indexOf(';', i + 2);
        weight += 1;
        i = end < 0 ? entity.value.length : end + 1;
        continue;
      }
      const parsed = readName(entity.value, i + 1);
      if (parsed.name !== '' && entity.value[parsed.next] === ';') {
        const child = this.entities.get(parsed.name);
        weight += child === undefined ? parsed.name.length + 2 : this.computeWeight(parsed.name);
        i = parsed.next + 1;
      } else {
        weight += 1;
        i += 1;
      }
    }
    const capped = Math.min(weight, this.quotas.maxTotalExpandedChars + 1);
    this.expansionWeight.set(name, capped);
    return capped;
  }

  /**
   * 取引用链末端叶子实体的原始值长度，作为“展开倍数”的基准分母。
   * 多叶子时取最小值，保证放大倍数的估计偏保守（更易触发熔断）。
   */
  private leafRawLength(name: string): number {
    const cached = this.leafLengthCache.get(name);
    if (cached !== undefined) {
      return cached;
    }
    const entity = this.entities.get(name);
    if (entity === undefined) {
      return 1;
    }
    let minimum = Number.POSITIVE_INFINITY;
    let sawReference = false;
    let i = 0;
    while (i < entity.value.length) {
      if (entity.value[i] !== '&') {
        i += 1;
        continue;
      }
      if (entity.value[i + 1] === '#') {
        const end = entity.value.indexOf(';', i + 2);
        i = end < 0 ? entity.value.length : end + 1;
        continue;
      }
      const parsed = readName(entity.value, i + 1);
      if (parsed.name !== '' && entity.value[parsed.next] === ';') {
        const child = this.entities.get(parsed.name);
        if (child !== undefined) {
          sawReference = true;
          minimum = Math.min(minimum, this.leafRawLength(parsed.name));
        }
        i = parsed.next + 1;
      } else {
        i += 1;
      }
    }
    if (!sawReference) {
      const result = Math.max(1, entity.value.length);
      this.leafLengthCache.set(name, result);
      return result;
    }
    const result = minimum === Number.POSITIVE_INFINITY ? 1 : minimum;
    this.leafLengthCache.set(name, result);
    return result;
  }

  get totalExpandedChars(): number {
    return this.totalReferenceChars;
  }

  /** 验证任意一段含引用文本（元素文本或属性值），展开结果在配额内可完全消费。 */
  verify(text: string): void {
    this.frames.push({ cap: this.quotas.maxTotalExpandedChars, emitted: 0 });
    try {
      this.processSegments(text);
    } finally {
      this.frames.pop();
    }
  }

  private emit(length: number, offset: number): void {
    const frame = this.frames[this.frames.length - 1] as ExpansionFrame;
    if (frame.emitted + length > frame.cap) {
      // 根帧预算即文档总配额；其余实体帧超出意味着单实体倍数被击穿。
      if (this.frames.length === 1) {
        throw new XmlSecurityError(
          'ENTITY_EXPANSION_TOTAL_EXCEEDED',
          `total entity expansion exceeded ${this.quotas.maxTotalExpandedChars} characters`,
          offset,
        );
      }
      throw new XmlSecurityError(
        'ENTITY_EXPANSION_MULTIPLIER_EXCEEDED',
        `single-entity expansion multiplier exceeded ${this.quotas.maxExpansionMultiplier}`,
        offset,
      );
    }
    if (this.totalReferenceChars + length > this.quotas.maxTotalExpandedChars) {
      throw new XmlSecurityError(
        'ENTITY_EXPANSION_TOTAL_EXCEEDED',
        `total entity expansion exceeded ${this.quotas.maxTotalExpandedChars} characters`,
        offset,
      );
    }
    frame.emitted += length;
    this.totalReferenceChars += length;
  }

  private processSegments(text: string): void {
    let literalStart = 0;
    let i = 0;
    while (i < text.length) {
      if (text[i] !== '&') {
        i++;
        continue;
      }
      if (i > literalStart) {
        this.emit(i - literalStart, literalStart);
      }
      const after = this.consumeReference(text, i);
      i = after;
      literalStart = after;
    }
    if (literalStart < text.length) {
      this.emit(text.length - literalStart, literalStart);
    }
  }

  /** 消费一个以 '&' 起始的引用，返回 ';' 之后的位置。 */
  private consumeReference(text: string, start: number): number {
    if (text[start + 1] === '#') {
      const end = text.indexOf(';', start + 2);
      if (end < 0) {
        throw new XmlSecurityError('XML_MALFORMED', 'unterminated character reference', start);
      }
      const cp = parseCharacterReference(text.slice(start + 2, end), start);
      const length = cp > 0xffff ? 2 : 1;
      this.emit(length, start);
      return end + 1;
    }

    const parsed = readName(text, start + 1);
    if (parsed.name === '' || text[parsed.next] !== ';') {
      throw new XmlSecurityError('XML_MALFORMED', 'malformed entity reference (expected &name;)', start);
    }
    const name = parsed.name;
    const after = parsed.next + 1;

    const predefined = PREDEFINED_ENTITIES.get(name);
    if (predefined !== undefined) {
      this.emit(predefined.length, start);
      return after;
    }

    const entity = this.entities.get(name);
    if (entity === undefined) {
      throw new XmlSecurityError('ENTITY_UNDECLARED', `undeclared general entity "${name}"`, start);
    }

    const depth = this.frames.length; // 根实体引用深度为 1
    if (depth > this.quotas.maxExpansionDepth) {
      throw new XmlSecurityError(
        'ENTITY_EXPANSION_DEPTH_EXCEEDED',
        `entity nesting depth exceeded ${this.quotas.maxExpansionDepth}`,
        start,
      );
    }

    const rawLength = Math.max(1, entity.value.length);
    const fullWeight = this.computeWeight(name);
    const baseLength = this.leafRawLength(name);
    if (fullWeight > baseLength * this.quotas.maxExpansionMultiplier) {
      throw new XmlSecurityError(
        'ENTITY_EXPANSION_MULTIPLIER_EXCEEDED',
        `entity "${name}" expansion weight ${fullWeight} exceeds ${this.quotas.maxExpansionMultiplier}x base length ${baseLength}`,
        start,
      );
    }
    const nextDepth = this.frames.length + 1;
    if (nextDepth - 1 > this.quotas.maxExpansionDepth) {
      throw new XmlSecurityError(
        'ENTITY_EXPANSION_DEPTH_EXCEEDED',
        `entity nesting depth exceeded ${this.quotas.maxExpansionDepth}`,
        start,
      );
    }
    this.frames.push({ cap: baseLength * this.quotas.maxExpansionMultiplier, emitted: 0 });
    try {
      this.processSegments(entity.value);
    } finally {
      this.frames.pop();
    }
    return after;
  }
}

function parseCharacterReference(body: string, offset: number): number {
  let cp: number;
  if (body[0] === 'x' || body[0] === 'X') {
    if (!/^[0-9a-fA-F]+$/.test(body.slice(1))) {
      throw new XmlSecurityError('CHARACTER_REFERENCE_INVALID', 'malformed hexadecimal character reference', offset);
    }
    cp = parseInt(body.slice(1), 16);
  } else {
    if (!/^[0-9]+$/.test(body)) {
      throw new XmlSecurityError('CHARACTER_REFERENCE_INVALID', 'malformed decimal character reference', offset);
    }
    cp = parseInt(body, 10);
  }
  if (
    cp === 0 ||
    cp > 0x10ffff ||
    (cp >= 0xd800 && cp <= 0xdfff) ||
    (cp >= 0x1 && cp <= 0x8) ||
    cp === 0xb ||
    (cp >= 0xe && cp <= 0x1f) ||
    (cp >= 0x7f && cp <= 0x9f)
  ) {
    throw new XmlSecurityError('CHARACTER_REFERENCE_INVALID', `illegal character reference U+${cp.toString(16).toUpperCase()}`, offset);
  }
  return cp;
}
