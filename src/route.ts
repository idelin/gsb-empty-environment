/**
 * 组合式路由构造库。
 *
 * 路径模式语法：
 *   /users            静态段
 *   /users/:id        必填参数段
 *   /users/:id?       可选参数段（省略时整段不出现）
 *
 * 规则：
 * - 同一有效路径上不允许重复的参数名（构造 / extend 时即抛错）。
 * - 可选父段省略时，子段参数仍按自身段位置独立取值，互不挤占。
 * - build 产出的 URL 保证可被 match 反解析为同一路由与同一组参数；
 *   若参数值会导致歧义（如可选参数值与后续静态段文本相同），build 直接抛错。
 * - 参数值逐个段做 percent-encode / decode，保留字符不会破坏段结构。
 * - 尾斜杠：模式中的尾斜杠被忽略；匹配 URL 时允许并忽略尾部斜杠；
 *   build 产出的 URL 永不带尾斜杠（根路由为 "/"）。
 */

export type Segment =
  | { readonly kind: "static"; readonly text: string }
  | { readonly kind: "param"; readonly name: string; readonly optional: boolean };

export type Params = Record<string, string>;

const PARAM_NAME = /^[A-Za-z_][A-Za-z0-9_]*$/;

function parsePattern(pattern: string): Segment[] {
  if (typeof pattern !== "string") {
    throw new TypeError(`Route pattern must be a string, got ${typeof pattern}`);
  }
  const trimmed = pattern.replace(/^\/+|\/+$/g, "");
  if (trimmed === "") return [];
  return trimmed.split("/").map((part): Segment => {
    if (part === "") {
      throw new Error(`Invalid route pattern "${pattern}": empty segment (double slash)`);
    }
    if (part.startsWith(":")) {
      const optional = part.endsWith("?");
      const name = part.slice(1, optional ? -1 : undefined);
      if (!PARAM_NAME.test(name)) {
        throw new Error(
          `Invalid route pattern "${pattern}": bad parameter name "${name}"`,
        );
      }
      return { kind: "param", name, optional };
    }
    return { kind: "static", text: part };
  });
}

function formatPath(segments: readonly Segment[]): string {
  return (
    "/" +
    segments
      .map((seg) =>
        seg.kind === "static"
          ? seg.text
          : `:${seg.name}${seg.optional ? "?" : ""}`,
      )
      .join("/")
  );
}

function safeDecode(part: string): string | null {
  try {
    return decodeURIComponent(part);
  } catch {
    return null;
  }
}

function paramsEqual(a: Params, b: Params): boolean {
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => a[key] === b[key]);
}

export class Route {
  readonly segments: readonly Segment[];
  readonly paramNames: readonly string[];
  readonly path: string;

  private constructor(segments: readonly Segment[]) {
    const seen = new Set<string>();
    for (const seg of segments) {
      if (seg.kind !== "param") continue;
      if (seen.has(seg.name)) {
        throw new Error(
          `Duplicate parameter name ":${seg.name}" on effective path "${formatPath(segments)}"`,
        );
      }
      seen.add(seg.name);
    }
    for (let i = 0; i + 1 < segments.length; i += 1) {
      const current = segments[i];
      const next = segments[i + 1];
      if (
        current.kind === "param" &&
        current.optional &&
        next.kind === "param" &&
        next.optional
      ) {
        throw new Error(
          `Ambiguous route pattern "${formatPath(segments)}": consecutive optional segments ` +
            `":${current.name}?" and ":${next.name}?" cannot be told apart when parsing`,
        );
      }
    }
    this.segments = segments;
    this.paramNames = [...seen];
    this.path = formatPath(segments);
  }

  /** 用路径模式创建路由，如 route("/users/:id")。 */
  static of(pattern: string): Route {
    return new Route(parsePattern(pattern));
  }

  /** 拼接子路径，返回新路由；组合后参数名重复会在此时抛错。 */
  extend(childPattern: string): Route {
    return new Route([...this.segments, ...parsePattern(childPattern)]);
  }

  /**
   * 用参数构造 URL。
   * - 缺少必填参数、必填参数为空串、传入未知参数：抛错。
   * - 可选参数为 undefined 或空串：该段省略。
   * - 产出的 URL 会自检可被 match 反解析为同一组参数，否则抛歧义错误。
   */
  build(params: Params = {}): string {
    const known = new Set(this.paramNames);
    for (const key of Object.keys(params)) {
      if (!known.has(key)) {
        throw new Error(`Unknown parameter "${key}" for route "${this.path}"`);
      }
      if (typeof params[key] !== "string") {
        throw new TypeError(
          `Parameter "${key}" for route "${this.path}" must be a string`,
        );
      }
    }

    const parts: string[] = [];
    const expected: Params = {};
    for (const seg of this.segments) {
      if (seg.kind === "static") {
        parts.push(seg.text);
        continue;
      }
      const value = params[seg.name];
      if (value === undefined || (seg.optional && value === "")) {
        if (seg.optional) continue;
        throw new Error(
          `Missing required parameter ":${seg.name}" for route "${this.path}"`,
        );
      }
      if (value === "") {
        throw new Error(
          `Parameter ":${seg.name}" for route "${this.path}" must not be an empty string`,
        );
      }
      parts.push(encodeURIComponent(value));
      expected[seg.name] = value;
    }

    const url = "/" + parts.join("/");
    const parsed = this.match(url);
    if (parsed === null || !paramsEqual(parsed, expected)) {
      throw new Error(
        `Ambiguous route "${this.path}": built URL "${url}" parses back to ` +
          `${JSON.stringify(parsed)} instead of ${JSON.stringify(expected)}; ` +
          `choose parameter values that do not collide with static segments`,
      );
    }
    return url;
  }

  /**
   * 反解析 URL，命中返回参数对象，否则返回 null。
   * 允许一个前导斜杠与任意尾部斜杠；忽略 query/hash；段内做 percent-decode。
   */
  match(url: string): Params | null {
    const path = url.replace(/[?#].*$/, "").replace(/^\/+|\/+$/g, "");
    const parts = path === "" ? [] : path.split("/");
    if (parts.some((part) => part === "")) return null;

    const segments = this.segments;
    const result: Params = {};

    // 可选段优先按“省略”匹配（与 build 的规范化输出一致），失败再回溯为“出现”。
    const walk = (segIndex: number, partIndex: number): boolean => {
      if (segIndex === segments.length) return partIndex === parts.length;
      const seg = segments[segIndex];
      if (seg.kind === "static") {
        return (
          partIndex < parts.length &&
          parts[partIndex] === seg.text &&
          walk(segIndex + 1, partIndex + 1)
        );
      }
      if (seg.optional && walk(segIndex + 1, partIndex)) return true;
      if (partIndex < parts.length) {
        const decoded = safeDecode(parts[partIndex]);
        if (decoded === null) return false;
        result[seg.name] = decoded;
        if (walk(segIndex + 1, partIndex + 1)) return true;
        delete result[seg.name];
      }
      return false;
    };

    return walk(0, 0) ? result : null;
  }
}

/** route("/users/:id") 等价于 Route.of(...)。 */
export function route(pattern: string): Route {
  return Route.of(pattern);
}
