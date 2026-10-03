// SQL 词法分析器

export type TokenType = 'ident' | 'number' | 'string' | 'symbol' | 'eof';

export interface Token {
  type: TokenType;
  value: string; // 保留原始大小写；关键字比较由解析器做大写归一
  pos: number;
}

const SYMBOLS = new Set(['(', ')', ',', '.', '*', '=', '<', '>', '!', ';']);

export function tokenize(sql: string): Token[] {
  const tokens: Token[] = [];
  let i = 0;

  const push = (type: TokenType, value: string, pos: number) =>
    tokens.push({ type, value, pos });

  while (i < sql.length) {
    const ch = sql[i];

    if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
      i++;
      continue;
    }
    // 行注释 -- ...
    if (ch === '-' && sql[i + 1] === '-') {
      while (i < sql.length && sql[i] !== '\n') i++;
      continue;
    }
    if (/[0-9]/.test(ch)) {
      const start = i;
      while (i < sql.length && /[0-9.]/.test(sql[i])) i++;
      push('number', sql.slice(start, i), start);
      continue;
    }
    if (ch === "'") {
      const start = i;
      i++;
      let out = '';
      while (i < sql.length && sql[i] !== "'") {
        if (sql[i] === "'" && sql[i + 1] === "'") {
          out += "'";
          i += 2;
        } else {
          out += sql[i];
          i++;
        }
      }
      if (i >= sql.length) throw new SyntaxError(`未闭合的字符串字面量，位置 ${start}`);
      i++; // 跳过结尾引号
      push('string', out, start);
      continue;
    }
    if (/[A-Za-z_]/.test(ch)) {
      const start = i;
      while (i < sql.length && /[A-Za-z0-9_$]/.test(sql[i])) i++;
      push('ident', sql.slice(start, i), start);
      continue;
    }
    // 两字符运算符 <> <= >= !=
    const two = sql.slice(i, i + 2);
    if (two === '<>' || two === '<=' || two === '>=' || two === '!=') {
      push('symbol', two, i);
      i += 2;
      continue;
    }
    if (SYMBOLS.has(ch)) {
      push('symbol', ch, i);
      i++;
      continue;
    }
    throw new SyntaxError(`无法识别的字符 '${ch}'，位置 ${i}`);
  }

  push('eof', '', sql.length);
  return tokens;
}
