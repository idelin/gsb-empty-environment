export type FieldType = 'string' | 'varint' | 'bool' | 'bytes';

export interface FieldSchema {
  index: number;
  name: string;
  type: FieldType;
}

export interface MessageSchema {
  typeUrl: string;
  fields: FieldSchema[];
}

/**
 * 版本化 schema 注册表。
 * 每次 register 使版本号 +1；同一 typeUrl 在新版本下可以有不同的字段含义。
 */
export class SchemaRegistry {
  #schemas = new Map<string, MessageSchema>();
  #version = 0;

  get version(): number {
    return this.#version;
  }

  register(schema: MessageSchema): number {
    this.#schemas.set(schema.typeUrl, schema);
    this.#version += 1;
    return this.#version;
  }

  lookup(typeUrl: string): MessageSchema | undefined {
    return this.#schemas.get(typeUrl);
  }
}
