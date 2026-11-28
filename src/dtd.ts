import { XmlSecurityError } from './errors.ts';
import { readName, skipWhitespace, skipComment, startsWithCi } from './xml-lex.ts';

export type DtdMode = 'strict' | 'internal-entities';

export interface GeneralEntity {
  name: string;
  /** 实体值原文（不含外层引号）。 */
  value: string;
}

export interface DtdParseResult {
  /** 仅包含内部通用实体；受控模式允许，零 DTD 模式不会到达这里。 */
  entities: Map<string, GeneralEntity>;
}

/**
 * 入口：text[start] 指向 '<'，且后续为不区分大小写的 '<!DOCTYPE'。
 * 返回 DTD 结束（'>'）后的偏移与解析出的内部通用实体。
 */
export function parseDocumentType(
  text: string,
  start: number,
  mode: DtdMode,
): { next: number; result: DtdParseResult } {
  if (mode === 'strict') {
    throw new XmlSecurityError('DTD_STRICT_MODE', 'any <!DOCTYPE ...> is forbidden in zero-DTD strict mode', start);
  }

  let i = start + '<!DOCTYPE'.length;
  const root = readName(text, skipWhitespace(text, i));
  if (root.name === '') {
    throw new XmlSecurityError('DTD_MALFORMED', 'DOCTYPE requires a root element name', start);
  }
  i = root.next;

  const entities = new Map<string, GeneralEntity>();

  while (i < text.length) {
    const afterWhitespace = skipWhitespace(text, i);
    if (afterWhitespace !== i) {
      i = afterWhitespace;
      continue;
    }
    if (text[i] === '[') {
      i = parseInternalSubset(text, i + 1, entities);
      const close = skipWhitespace(text, i);
      if (text[close] !== '>') {
        throw new XmlSecurityError('DTD_MALFORMED', 'expected ">" after internal DTD subset', close);
      }
      return { next: close + 1, result: { entities } };
    }
    if (text[i] === '>') {
      return { next: i + 1, result: { entities } };
    }
    if (startsWithCi(text, i, 'SYSTEM') || startsWithCi(text, i, 'PUBLIC')) {
      throw new XmlSecurityError(
        'DTD_EXTERNAL_REFERENCE',
        'external DTD SYSTEM/PUBLIC identifiers are forbidden',
        i,
      );
    }
    throw new XmlSecurityError('DTD_MALFORMED', 'unexpected token in DOCTYPE declaration', i);
  }

  throw new XmlSecurityError('DTD_MALFORMED', 'unterminated DOCTYPE declaration', start);
}

/** 解析 '[' 与 ']' 之间的内部子集，返回 ']' 之后的位置。 */
function parseInternalSubset(
  text: string,
  start: number,
  entities: Map<string, GeneralEntity>,
): number {
  let i = start;
  while (i < text.length) {
    const afterWhitespace = skipWhitespace(text, i);
    i = afterWhitespace;
    if (text[i] === ']') {
      return i + 1;
    }
    if (startsWithCi(text, i, '<!--')) {
      i = skipComment(text, i);
      continue;
    }
    if (startsWithCi(text, i, '<!ENTITY')) {
      i = parseEntityDeclaration(text, i, entities);
      continue;
    }
    if (
      startsWithCi(text, i, '<!ELEMENT') ||
      startsWithCi(text, i, '<!ATTLIST') ||
      startsWithCi(text, i, '<!NOTATION')
    ) {
      throw new XmlSecurityError(
        'DTD_UNSUPPORTED_CONSTRUCT',
        'only internal general entity declarations are allowed in the DTD subset',
        i,
      );
    }
    if (text[i] === '<') {
      throw new XmlSecurityError('DTD_MALFORMED', 'unsupported declaration inside internal DTD subset', i);
    }
    if (text[i] === '%') {
      throw new XmlSecurityError(
        'DTD_PARAMETER_ENTITY',
        'parameter entity references are forbidden in the DTD subset',
        i,
      );
    }
    throw new XmlSecurityError('DTD_MALFORMED', 'unexpected character in internal DTD subset', i);
  }
  throw new XmlSecurityError('DTD_MALFORMED', 'unterminated internal DTD subset');
}

/**
 * 解析单个 <!ENTITY ...> 声明。
 * 仅允许内部通用实体：<!ENTITY name "value"> / <!ENTITY name 'value'>。
 */
function parseEntityDeclaration(
  text: string,
  start: number,
  entities: Map<string, GeneralEntity>,
): number {
  let i = start + '<!ENTITY'.length;
  i = skipWhitespace(text, i);

  if (text[i] === '%') {
    throw new XmlSecurityError(
      'DTD_PARAMETER_ENTITY',
      'parameter entity declarations (%name;) are forbidden',
      i,
    );
  }

  const name = readName(text, i);
  if (name.name === '') {
    throw new XmlSecurityError('DTD_MALFORMED', 'ENTITY declaration requires a name', start);
  }
  i = skipWhitespace(text, name.next);

  if (startsWithCi(text, i, 'SYSTEM') || startsWithCi(text, i, 'PUBLIC')) {
    throw new XmlSecurityError(
      'DTD_EXTERNAL_REFERENCE',
      'external entities (SYSTEM/PUBLIC) are forbidden',
      i,
    );
  }

  const quote = text[i];
  if (quote !== '"' && quote !== "'") {
    throw new XmlSecurityError(
      'DTD_MALFORMED',
      'only quoted internal general entity values are allowed',
      i,
    );
  }
  const valueStart = i + 1;
  const valueEnd = text.indexOf(quote, valueStart);
  if (valueEnd < 0) {
    throw new XmlSecurityError('DTD_MALFORMED', 'unterminated entity value', start);
  }
  const value = text.slice(valueStart, valueEnd);
  if (value.includes('%')) {
    throw new XmlSecurityError(
      'DTD_PARAMETER_ENTITY',
      'parameter entity references are forbidden inside entity values',
      valueStart,
    );
  }
  if (entities.has(name.name)) {
    throw new XmlSecurityError('DTD_UNSUPPORTED_CONSTRUCT', `duplicate entity declaration "${name.name}"`, start);
  }
  entities.set(name.name, { name: name.name, value });

  i = valueEnd + 1;
  const closeOffset = skipWhitespace(text, i);
  if (text[closeOffset] !== '>') {
    throw new XmlSecurityError('DTD_MALFORMED', 'expected ">" to close ENTITY declaration', i);
  }
  return closeOffset + 1;
}
