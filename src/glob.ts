const REGEXP_SPECIALS = '.+^${}()|\\';

function escapeChar(ch : string) : string {
  if (REGEXP_SPECIALS.indexOf(ch) !== -1) return '\\' + ch;
  return ch;
}

function normalizePath(path : string) : string {
  let out = path;
  while (out.startsWith('./')) out = out.slice(2);
  if (out.charAt(0) === '/') out = out.slice(1);
  return out;
}

export function globToRegExp(glob : string) : RegExp {
  let source = '';
  let i = 0;
  while (i < glob.length) {
    const ch = glob.charAt(i);
    if (ch === '*') {
      if (glob.charAt(i + 1) === '*') {
        if (glob.charAt(i + 2) === '/') {
          source += '(?:.*/)?';
          i += 3;
        } else {
          source += '.*';
          i += 2;
        }
      } else {
        source += '[^/]*';
        i += 1;
      }
    } else if (ch === '?') {
      source += '[^/]';
      i += 1;
    } else if (ch === '[') {
      const close = glob.indexOf(']', i + 1);
      if (close === -1) {
        source += '\\[';
        i += 1;
      } else {
        let cls = glob.slice(i + 1, close);
        if (cls.charAt(0) === '!') cls = '^' + cls.slice(1);
        source += '[' + cls.split('\\').join('\\\\') + ']';
        i = close + 1;
      }
    } else {
      source += escapeChar(ch);
      i += 1;
    }
  }
  return new RegExp('^' + source + '$');
}

export function matchPath(pattern : string, path : string) : boolean {
  let pat = pattern;
  if (pat.charAt(0) === '/') pat = pat.slice(1);
  if (pat.endsWith('/')) pat = pat + '**';
  const target = normalizePath(path);
  if (pat.indexOf('/') === -1) {
    const slash = target.lastIndexOf('/');
    let base = target;
    if (slash !== -1) base = target.slice(slash + 1);
    return globToRegExp(pat).test(base);
  }
  return globToRegExp(pat).test(target);
}
