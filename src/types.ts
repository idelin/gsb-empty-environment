/** JSON 兼容值。 */
export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

/** 树内路径：对象键为 string，数组下标为 number。 */
export type Path = ReadonlyArray<string | number>;

/** 把路径渲染成 `$.a[0].b` 形式，便于错误定位。 */
export function pathToString(path: Path): string {
  let out = '$';
  for (const seg of path) {
    if (typeof seg === 'number') {
      out += `[${seg}]`;
    } else if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(seg)) {
      out += `.${seg}`;
    } else {
      out += `[${JSON.stringify(seg)}]`;
    }
  }
  return out;
}

export function pathKey(path: Path): string {
  return JSON.stringify(path);
}

export function isObject(v: JsonValue): v is { [key: string]: JsonValue } {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

export function deepClone<T extends JsonValue>(v: T): T {
  if (Array.isArray(v)) return v.map((x) => deepClone(x)) as T;
  if (isObject(v)) {
    const out: { [key: string]: JsonValue } = {};
    for (const k of Object.keys(v)) out[k] = deepClone(v[k]);
    return out as T;
  }
  return v;
}
