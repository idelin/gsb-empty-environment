/** 字段定义：关系模式（Schema）中的一列。 */
export interface Field {
  name: string;
  type: string;
}

/** 算子输出模式 = 有序字段列表。 */
export type Schema = Field[];

export function fieldNames(schema: Schema): string[] {
  return schema.map((f) => f.name);
}

/** 构造一张 n 列的宽表模式：id, name, col_03 ... col_n */
export function makeWideSchema(columnCount: number): Schema {
  if (columnCount < 2) {
    throw new Error("columnCount must be >= 2 to include id and name");
  }
  const schema: Schema = [
    { name: "id", type: "BIGINT" },
    { name: "name", type: "VARCHAR" },
  ];
  for (let i = 3; i <= columnCount; i++) {
    schema.push({ name: `col_${String(i).padStart(2, "0")}`, type: "VARCHAR" });
  }
  return schema;
}
