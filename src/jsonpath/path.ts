/** 规范路径段：对象键为 string（键本身无序），数组下标为 number（有序）。 */
export type PathSeg = string | number;
export type Path = PathSeg[];

/**
 * 规范路径序列化：一律使用括号记法，键经 JSON 转义，
 * 因此含斜杠/引号/空键等特殊键都能精确往返。
 * 例：$["a/b"][0]["x"]
 */
export function canonical(path: Path): string {
  let out = '$';
  for (const seg of path) {
    out += typeof seg === 'number' ? `[${seg}]` : `[${JSON.stringify(seg)}]`;
  }
  return out;
}

/** canonical 的逆运算，用于测试与诊断。 */
export function parseCanonical(s: string): Path {
  if (!s.startsWith('$')) throw new Error(`bad canonical path: ${s}`);
  const out: Path = [];
  let i = 1;
  while (i < s.length) {
    if (s[i] !== '[') throw new Error(`bad canonical path at ${i}: ${s}`);
    i++;
    if (s[i] === '"') {
      let j = i;
      let escaped = false;
      while (j < s.length) {
        if (!escaped && s[j] === '"') break;
        escaped = !escaped && s[j] === '\\';
        if (s[j] !== '\\') escaped = false;
        j++;
      }
      out.push(JSON.parse(s.slice(i, j + 1)) as string);
      i = j + 2; // 跳过 "] 后的 ]
    } else {
      const m = /^-?\d+/.exec(s.slice(i));
      if (!m) throw new Error(`bad canonical path at ${i}: ${s}`);
      out.push(Number(m[0]));
      i += m[0].length + 1;
    }
  }
  return out;
}

/** a 是否为 b 的前缀（含相等）。 */
export function isPrefix(a: Path, b: Path): boolean {
  if (a.length > b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}
