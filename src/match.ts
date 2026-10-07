import type { MatchRecord } from './types.js';

// 在旧文本上一次性确定全部非重叠匹配（从左到右）。
// 零宽匹配命中后扫描位置前进一个码点，因此每个边界最多命中一次。
// 正则无论是否带 g 标志，都按全局语义处理。
export function findAllMatches(text: string, pattern: RegExp | string): MatchRecord[] {
  if (typeof pattern === 'string') {
    return findStringMatches(text, pattern);
  }
  return findRegexMatches(text, pattern);
}

function findStringMatches(text: string, needle: string): MatchRecord[] {
  const out: MatchRecord[] = [];
  if (needle.length === 0) {
    for (let i = 0; i <= text.length; i++) {
      const rec: MatchRecord = { start: i, end: i, text: '', groups: [], namedGroups: undefined };
      out.push(rec);
    }
    return out;
  }
  let from = 0;
  while (from <= text.length) {
    const idx = text.indexOf(needle, from);
    if (idx === -1) {
      break;
    }
    const rec: MatchRecord = { start: idx, end: idx + needle.length, text: needle, groups: [], namedGroups: undefined };
    out.push(rec);
    from = idx + needle.length;
  }
  return out;
}

function findRegexMatches(text: string, pattern: RegExp): MatchRecord[] {
  const flags = pattern.global ? pattern.flags : pattern.flags + 'g';
  const re = new RegExp(pattern.source, flags);
  const out: MatchRecord[] = [];
  let m = re.exec(text);
  while (m !== null) {
    const matched = m[0];
    const groups: Array<string | undefined> = [];
    for (let k = 1; k < m.length; k++) {
      groups.push(m[k]);
    }
    const rec: MatchRecord = {
      start: m.index,
      end: m.index + matched.length,
      text: matched,
      groups,
      namedGroups: m.groups,
    };
    out.push(rec);
    if (matched.length === 0) {
      const cp = text.codePointAt(re.lastIndex);
      if (re.unicode && cp !== undefined && cp > 0xffff) {
        re.lastIndex += 2;
      } else {
        re.lastIndex += 1;
      }
    }
    m = re.exec(text);
  }
  return out;
}
