export type JsonValue =
  | null
  | boolean
  | number
  | string
  | JsonValue[]
  | { [key: string]: JsonValue };

export type JsonObject = { [key: string]: JsonValue };

export type PathSegment = string | number;

export interface InsertOp {
  op: 'insert';
  id: string;
  parentId: string | null;
  key: string;
  index: number;
  node: JsonObject;
}

export interface MoveOp {
  op: 'move';
  id: string;
  parentId: string | null;
  key: string;
  index: number;
}

export interface RemoveOp {
  op: 'remove';
  id: string;
}

export interface UpdateSetOp {
  op: 'update';
  id: string;
  path: PathSegment[];
  value: JsonValue;
}

export interface UpdateUnsetOp {
  op: 'update';
  id: string;
  path: PathSegment[];
  unset: true;
}

export type Operation = InsertOp | MoveOp | RemoveOp | UpdateSetOp | UpdateUnsetOp;

/** Error thrown for invalid input trees or inapplicable patches. `path` locates the source. */
export class TreeDiffError extends Error {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`${message} (at ${path === '' ? '/' : path})`);
    this.name = 'TreeDiffError';
    this.path = path;
  }
}
