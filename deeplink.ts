/**
 * 深链校验组件：可嵌入路由层，保证校验与导航使用同一份规范化结果。
 *
 * 规则：
 * - 仅接受 scheme 为 myapp 与 https；
 * - 主机转小写并转 punycode 后与白名单精确匹配；
 * - 含用户信息、反斜杠、控制字符与 NUL 一律拒绝；
 * - query 中 url 参数递归通过同一校验，嵌套不超过 2 层；
 * - 路径先解码一次，再按 RFC 3986 消去点段；
 * - 返回给导航层的就是规范化后的 URL 字符串。
 */

export const MAX_NESTING = 2;

const ALLOWED_SCHEMES = new Set(['myapp:', 'https:']);
const CONTROL_CHARS = /[\u0000-\u001f\u007f]/;

export type ValidateResult =
  | { ok: true; url: string }
  | { ok: false; reason: string };

function fail(reason: string): ValidateResult {
  return { ok: false, reason };
}

/* ---------- RFC 3492 Punycode 编码（单标签） ---------- */

const BASE = 36;
const TMIN = 1;
const TMAX = 26;
const SKEW = 38;
const DAMP = 700;
const INITIAL_BIAS = 72;
const INITIAL_N = 128;

function adapt(delta: number, numPoints: number, firstTime: boolean): number {
  delta = firstTime ? Math.floor(delta / DAMP) : delta >> 1;
  delta += Math.floor(delta / numPoints);
  let k = 0;
  while (delta > Math.floor(((BASE - TMIN) * TMAX) / 2)) {
    delta = Math.floor(delta / (BASE - TMIN));
    k += BASE;
  }
  return k + Math.floor(((BASE - TMIN + 1) * delta) / (delta + SKEW));
}

function digitToBasic(d: number): string {
  // 0..25 -> a..z，26..35 -> 0..9
  return String.fromCharCode(d + 22 + (d < 26 ? 75 : 0));
}

export function punycodeEncode(input: string): string {
  const codePoints = [...input].map((c) => c.codePointAt(0)!);
  let n = INITIAL_N;
  let delta = 0;
  let bias = INITIAL_BIAS;
  let output = '';
  const basicCount = codePoints.filter((cp) => cp < 0x80).length;
  for (const cp of codePoints) {
    if (cp < 0x80) output += String.fromCharCode(cp);
  }
  let h = basicCount;
  if (basicCount > 0 && h < codePoints.length) output += '-';
  while (h < codePoints.length) {
    let m = Infinity;
    for (const cp of codePoints) {
      if (cp >= n && cp < m) m = cp;
    }
    delta += (m - n) * (h + 1);
    n = m;
    for (const cp of codePoints) {
      if (cp < n) {
        delta++;
      } else if (cp === n) {
        let q = delta;
        for (let k = BASE; ; k += BASE) {
          const t = k <= bias ? TMIN : k >= bias + TMAX ? TMAX : k - bias;
          if (q < t) break;
          output += digitToBasic(t + ((q - t) % (BASE - t)));
          q = Math.floor((q - t) / (BASE - t));
        }
        output += digitToBasic(q);
        bias = adapt(delta, h + 1, h === basicCount);
        delta = 0;
        h++;
      }
    }
    delta++;
    n++;
  }
  return output;
}

/* ---------- 主机规范化：小写 + IDNA ToASCII ---------- */

export function toAsciiHost(host: string): string | null {
  if (host === '') return null;
  const lower = host.toLowerCase();
  const labels = lower.split('.');
  const out: string[] = [];
  for (const label of labels) {
    if (label === '') return null; // 空标签（含末尾点）一律拒绝
    if (/^[\x00-\x7f]+$/.test(label)) {
      if (!/^[a-z0-9-]+$/.test(label)) return null;
      out.push(label);
    } else {
      if (label.includes('%')) return null;
      out.push('xn--' + punycodeEncode(label));
    }
  }
  return out.join('.');
}

/* ---------- 路径规范化：解码一次 + 消去点段 ---------- */

function removeDotSegments(path: string): string {
  let input = path;
  let output = '';
  while (input.length > 0) {
    if (input.startsWith('../')) {
      input = input.slice(3);
    } else if (input.startsWith('./')) {
      input = input.slice(2);
    } else if (input.startsWith('/./')) {
      input = '/' + input.slice(3);
    } else if (input === '/.') {
      input = '/';
    } else if (input.startsWith('/../')) {
      input = '/' + input.slice(4);
      output = output.replace(/\/[^/]*$/, '');
    } else if (input === '/..') {
      input = '/';
      output = output.replace(/\/[^/]*$/, '');
    } else if (input === '.' || input === '..') {
      input = '';
    } else {
      const match = input.match(/^\/?[^/]*/)!;
      output += match[0];
      input = input.slice(match[0].length);
    }
  }
  return output;
}

export function normalizePath(pathname: string): string | null {
  let decoded: string;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  // 解码一次后出现控制字符、NUL 或反斜杠，一律拒绝
  if (CONTROL_CHARS.test(decoded) || decoded.includes('\\')) return null;
  return removeDotSegments(decoded);
}

/* ---------- 主校验入口 ---------- */

export function validateDeepLink(
  raw: string,
  whitelist: readonly string[],
  depth = 1,
): ValidateResult {
  if (depth > MAX_NESTING) return fail(`嵌套超过 ${MAX_NESTING} 层`);
  if (typeof raw !== 'string' || raw.length === 0) return fail('空链接');
  if (CONTROL_CHARS.test(raw)) return fail('包含控制字符或 NUL');
  if (raw.includes('\\')) return fail('包含反斜杠');
  if (/\s/.test(raw)) return fail('包含空白字符');
  if (!/^[a-zA-Z][a-zA-Z0-9+.-]*:\/\//.test(raw)) return fail('缺少 scheme:// 前缀');

  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return fail('URL 解析失败');
  }

  if (!ALLOWED_SCHEMES.has(url.protocol)) return fail(`scheme 不允许: ${url.protocol}`);

  // 用户信息：原始 authority 中出现 @ 即拒绝（含空用户名），同时兜底解析结果
  const authority = raw.slice(raw.indexOf('://') + 3).split(/[/?#]/, 1)[0];
  if (authority.includes('@')) return fail('包含用户信息');
  if (url.username !== '' || url.password !== '') return fail('包含用户信息');

  const asciiHost = toAsciiHost(url.hostname);
  if (asciiHost === null) return fail('主机非法');
  if (!whitelist.includes(asciiHost)) return fail(`主机不在白名单: ${asciiHost}`);

  const normPath = normalizePath(url.pathname);
  if (normPath === null) return fail('路径非法');

  // url 参数递归校验，嵌套深度 +1
  const params = new URLSearchParams(url.search);
  const inner = params.get('url');
  if (inner !== null) {
    const innerResult = validateDeepLink(inner, whitelist, depth + 1);
    if (!innerResult.ok) return fail(`嵌套 url 非法: ${innerResult.reason}`);
    params.set('url', innerResult.url); // 导航层拿到的也是规范化结果
  }

  const port = url.port === '' ? '' : `:${url.port}`;
  const search = params.toString();
  const normalized =
    `${url.protocol}//${asciiHost}${port}${normPath}` +
    (search === '' ? '' : `?${search}`) +
    url.hash;
  return { ok: true, url: normalized };
}
