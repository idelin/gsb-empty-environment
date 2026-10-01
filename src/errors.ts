export type PathSegment = string | number;

export interface ValidationError {
  schemaVersion: string;
  rule: string;
  instancePath: PathSegment[];
  schemaPath: string;
  message: string;
  params?: Record<string, unknown>;
}

export function pathToString(path: readonly PathSegment[]): string {
  if (path.length === 0) return "";
  return (
    "/" +
    path
      .map((seg) => String(seg).replace(/~/g, "~0").replace(/\//g, "~1"))
      .join("/")
  );
}

export function parsePath(pointer: string): PathSegment[] {
  if (pointer === "" || pointer === "/") return [];
  const raw = pointer.startsWith("/") ? pointer.slice(1) : pointer;
  return raw.split("/").map((part) => {
    const seg = part.replace(/~1/g, "/").replace(/~0/g, "~");
    return /^\d+$/.test(seg) ? Number(seg) : seg;
  });
}

export function errorKey(err: ValidationError): string {
  return JSON.stringify([
    err.schemaVersion,
    err.rule,
    err.schemaPath,
    err.instancePath,
    err.message,
    err.params ?? null,
  ]);
}

export function toMultiset(errors: readonly ValidationError[]): Map<string, number> {
  const multiset = new Map<string, number>();
  for (const err of errors) {
    const key = errorKey(err);
    multiset.set(key, (multiset.get(key) ?? 0) + 1);
  }
  return multiset;
}
