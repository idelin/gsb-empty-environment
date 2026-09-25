/** JSON 兼容值：对象键无序、数组有序。 */
export type JSONValue =
  | null
  | boolean
  | number
  | string
  | JSONValue[]
  | { [key: string]: JSONValue };

/** 路径段：对象键用 string，数组下标用 number。 */
export type PathSegment = string | number;
export type Path = PathSegment[];

/** 一条证据 = 某个来源数据集内的一条具体路径。 */
export interface Evidence {
  readonly source: string;
  readonly path: Path;
}

/** 携带血缘证据的值。evidence 始终按 (source, path) 去重。 */
export interface Tracked<V extends JSONValue = JSONValue> {
  readonly value: V;
  readonly evidence: Evidence[];
}

/** 一行数据：字段名 -> 被追踪的值。 */
export type Row = Record<string, Tracked>;

/** 关系（有序行集合），算子的输入输出。 */
export interface Relation {
  readonly rows: Row[];
}
