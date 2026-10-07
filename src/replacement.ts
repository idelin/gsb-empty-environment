import type { MatchRecord } from './types.js';

// 展开替换模板。语义对齐 String.prototype.replace 的 $ 序列，
// 另外支持反斜杠转义：\n \t \r \\ \$；未知转义保留反斜杠原样。
// $` 与 $' 引用的是冻结的旧文本（因为全部匹配在旧文本上确定、同时应用）。
// 未参与的捕获组展开为空串；不存在的组引用按字面量保留。
export function expandReplacement(template: string, match: MatchRecord, source: string): string {
  let out = '';
  let i = 0;
  while (i < template.length) {
    const ch = template.charAt(i);
    if (ch === '\\' && i + 1 < template.length) {
      const esc = template.charAt(i + 1);
      if (esc === 'n') {
        out += '\n';
        i += 2;
        continue;
      }
      if (esc === 't') {
        out += '\t';
        i += 2;
        continue;
      }
      if (esc === 'r') {
        out += '\r';
        i += 2;
        continue;
      }
      if (esc === '\\') {
        out += '\\';
        i += 2;
        continue;
      }
      if (esc === '$') {
        out += '$';
        i += 2;
        continue;
      }
      out += '\\';
      i += 1;
      continue;
    }
    if (ch === '$' && i + 1 < template.length) {
      const next = template.charAt(i + 1);
      if (next === '$') {
        out += '$';
        i += 2;
        continue;
      }
      if (next === '&') {
        out += match.text;
        i += 2;
        continue;
      }
      if (next === '`') {
        out += source.slice(0, match.start);
        i += 2;
        continue;
      }
      if (next === "'") {
        out += source.slice(match.end);
        i += 2;
        continue;
      }
      if (next === '<') {
        const close = template.indexOf('>', i + 2);
        if (close !== -1) {
          const name = template.slice(i + 2, close);
          const named = match.namedGroups;
          if (named !== undefined && Object.prototype.hasOwnProperty.call(named, name)) {
            const v = named[name];
            if (v === undefined) {
              out += '';
            } else {
              out += v;
            }
            i = close + 1;
            continue;
          }
        }
        out += '$';
        i += 1;
        continue;
      }
      if (isDigit(next)) {
        const groupCount = match.groups.length;
        const two = template.slice(i + 1, i + 3);
        const twoNum = parseInt(two, 10);
        if (two.length === 2 && isDigit(two.charAt(1)) && twoNum >= 1 && twoNum <= groupCount) {
          out += groupValue(match.groups[twoNum - 1]);
          i += 3;
          continue;
        }
        const oneNum = parseInt(next, 10);
        if (oneNum >= 1 && oneNum <= groupCount) {
          out += groupValue(match.groups[oneNum - 1]);
          i += 2;
          continue;
        }
        out += '$';
        i += 1;
        continue;
      }
      out += '$';
      i += 1;
      continue;
    }
    out += ch;
    i += 1;
  }
  return out;
}

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

function groupValue(v: string | undefined): string {
  if (v === undefined) {
    return '';
  }
  return v;
}
