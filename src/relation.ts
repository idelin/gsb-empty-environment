import type { Evidence, JSONValue, Path, Relation, Row, Tracked } from "./types.ts";
import { PathError, formatPath } from "./errors.ts";
import { mergeEvidence } from "./lineage.ts";

function isObject(v: JSONValue): v is { [key: string]: JSONValue } {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

/** 把来源数据集（JSON 数组）包装成 Relation，每个顶层字段携带指向原始路径的证据。 */
export function trackSource(source: string, data: JSONValue[]): Relation {
  const rows: Row[] = data.map((rec, i) => {
    if (!isObject(rec)) {
      throw new PathError("source rows must be JSON objects", [i], source);
    }
    const row: Row = {};
    for (const key of Object.keys(rec)) {
      row[key] = { value: rec[key]!, evidence: [{ source, path: [i, key] }] };
    }
    return row;
  });
  return { rows };
}

/** 沿子路径下钻；单一证据时精确延长路径，多证据（派生值）时保留证据集。 */
export function drill(t: Tracked, sub: Path, at: Path): Tracked {
  let value: JSONValue = t.value;
  const walked: Path = [];
  for (const seg of sub) {
    if (typeof seg === "number" && Array.isArray(value) && seg >= 0 && seg < value.length) {
      value = value[seg]!;
    } else if (typeof seg === "string" && isObject(value) && seg in value) {
      value = value[seg]!;
    } else {
      throw new PathError(`missing segment ${JSON.stringify(seg)}`, [...at, ...walked, seg]);
    }
    walked.push(seg);
  }
  const evidence: Evidence[] =
    t.evidence.length === 1
      ? [{ source: t.evidence[0]!.source, path: [...t.evidence[0]!.path, ...sub] }]
      : [...t.evidence];
  return { value, evidence };
}

/** 取行字段；缺失时抛出带输出路径的 PathError。 */
export function field(row: Row, name: string, at: Path): Tracked {
  const t = row[name];
  if (t === undefined) throw new PathError(`missing field ${JSON.stringify(name)}`, [...at, name]);
  return t;
}

/** 行级证据 = 全部字段证据的并集（用于 count 等整行度量）。 */
export function rowEvidence(row: Row): Evidence[] {
  return mergeEvidence(...Object.values(row).map((t) => t.evidence));
}

export { formatPath };
