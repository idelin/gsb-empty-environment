// 极简 TypeScript 加载器：本环境无法安装 tsc，故在加载时剥离类型注解。
// 仅支持本仓库源码使用的受限 TS 子集，编码约定见 README：
//   - 类型名首字母大写（或是 string/number/boolean/void 等关键字）；
//   - 对象字面量属性值若为标识符必须小写开头（本仓库自然满足）；
//   - 不使用泛型调用、参数属性等高级语法。
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const IDENT = String.raw`[A-Za-z_$][A-Za-z0-9_$]*`;
const TYPE_HEAD = String.raw`(?:[A-Z][A-Za-z0-9_$]*|string|number|boolean|void|any|unknown|never)`;
const TYPE_ATOM = String.raw`(?:${TYPE_HEAD}(?:\.${IDENT})*(?:<[^;{}()<>]*>)?(?:\[\])*)`;
const TYPE_RE = String.raw`(?:${TYPE_ATOM}(?:\s*[|&]\s*${TYPE_ATOM})*)`;

const reInterface = /^[ \t]*(?:export[ \t]+)?interface[ \t]+\w+[^\n]*\{[^]*?^\}/gm;
const reTypeAlias = /^[ \t]*(?:export[ \t]+)?type[ \t]+\w+[^=\n]*=[^]*?;[ \t]*$/gm;
const reImportType = /^[ \t]*import[ \t]+type[ \t]+[^\n]*$/gm;
const reCastParen = new RegExp(String.raw`\(([^()]*?)\)[ \t]+as[ \t]+${TYPE_RE}`, 'g');
const reCastIdent = new RegExp(String.raw`\b(?:as[ \t]+)${TYPE_RE}`, 'g');
const reAnno = new RegExp(String.raw`:[ \t]*${TYPE_RE}[ \t]*(?=[,)\]=;{])`, 'g');
const reDefinite = new RegExp(String.raw`([,(][ \t]*${IDENT})!(?=[ \t]*:)`, 'g');
const reNewGeneric = new RegExp(String.raw`\bnew[ \t]+(${IDENT})<[^;{}()<>]*>`, 'g');

const STRING_RE = /('(?:[^'\\\n]|\\.)*'|"(?:[^"\\\n]|\\.)*"|`(?:[^`\\]|\\.)*`)/g;
const PLACEHOLDER = '""';

function blankStrings(code) {
  const strings = [];
  const blanked = code.replace(STRING_RE, (m) => {
    strings.push(m);
    return PLACEHOLDER;
  });
  return { blanked, strings };
}

function restoreStrings(code, strings) {
  let i = 0;
  return code.replace(/""/g, () => strings[i++]);
}

export function stripTypes(code) {
  // 结构性删除先行：这些行/块内部可能含字符串字面量（如字符串字面量类型），
  // 若在遮蔽字符串之后删除会导致占位符错位。
  const structural = code
    .replace(reImportType, '')
    .replace(reInterface, '')
    .replace(reTypeAlias, '');
  const { blanked, strings } = blankStrings(structural);
  const stripped = blanked
    .replace(reCastParen, '$1')
    .replace(reCastIdent, '')
    .replace(reDefinite, '$1')
    .replace(reNewGeneric, 'new $1')
    .replace(reAnno, '');
  return restoreStrings(stripped, strings);
}

export async function resolve(specifier, context, nextResolve) {
  if (specifier.endsWith('.js')) {
    try {
      return await nextResolve(specifier, context);
    } catch {
      const tsSpecifier = specifier.replace(/\.js$/, '.ts');
      return { url: new URL(tsSpecifier, context.parentURL).href, shortCircuit: true };
    }
  }
  return nextResolve(specifier, context);
}

export async function load(url, context, nextLoad) {
  if (url.endsWith('.ts')) {
    const source = readFileSync(fileURLToPath(url), 'utf8');
    return { format: 'module', source: stripTypes(source), shortCircuit: true };
  }
  return nextLoad(url, context);
}
