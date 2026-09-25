import type { Path } from "./types.ts";

/** 把路径格式化为 $.a[0].b 形式，对象键无序、数组有序。 */
export function formatPath(path: Path): string {
  let out = "$";
  for (const seg of path) {
    if (typeof seg === "number") out += `[${seg}]`;
    else if (/^[A-Za-z_][A-Za-z0-9_]*$/.test(seg)) out += `.${seg}`;
    else out += `[${JSON.stringify(seg)}]`;
  }
  return out;
}

/** 可定位到具体路径（及来源）的错误。 */
export class PathError extends Error {
  readonly path: Path;
  readonly source?: string;

  constructor(message: string, path: Path, source?: string) {
    const where = formatPath(path) + (source === undefined ? "" : ` in source ${JSON.stringify(source)}`);
    super(`${message} (at ${where})`);
    this.name = "PathError";
    this.path = [...path];
    this.source = source;
  }
}
