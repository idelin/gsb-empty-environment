export type ValueType =
  | 'i32'
  | 'i64'
  | 'f32'
  | 'f64'
  | 'v128'
  | 'funcref'
  | 'externref';

export interface FuncType {
  params: ValueType[];
  results: ValueType[];
}

export interface Limits {
  min: number;
  max: number | null;
  shared: boolean;
}

export interface TableType {
  elementType: 'funcref' | 'externref';
  limits: Limits;
}

export interface GlobalType {
  valueType: ValueType;
  mutable: boolean;
}

export interface ParsedFunction {
  index: number;
  typeIndex: number;
  imported: boolean;
}

export type WasmImport =
  | {
      kind: 'func';
      module: string;
      field: string;
      typeIndex: number;
      functionIndex: number;
    }
  | {
      kind: 'memory';
      module: string;
      field: string;
      limits: Limits;
    }
  | {
      kind: 'table';
      module: string;
      field: string;
      tableType: TableType;
    }
  | {
      kind: 'global';
      module: string;
      field: string;
      globalType: GlobalType;
    };

export interface StartSection {
  functionIndex: number;
}

export interface ParsedWasmModule {
  types: FuncType[];
  imports: WasmImport[];
  functions: ParsedFunction[];
  start: StartSection | null;
}

export interface FunctionCapability {
  kind: 'func';
  type: FuncType;
}

export interface MemoryCapability {
  kind: 'memory';
  limits: Limits;
}

export interface TableCapability {
  kind: 'table';
  tableType: TableType;
}

export interface GlobalCapability {
  kind: 'global';
  globalType: GlobalType;
}

export type ImportCapability =
  | FunctionCapability
  | MemoryCapability
  | TableCapability
  | GlobalCapability;

export interface ModuleCapabilities {
  allowStart?: boolean;
  fields: Record<string, ImportCapability>;
}

export interface CapabilityTable {
  allowStart?: boolean;
  modules: Record<string, ModuleCapabilities>;
}

export type IssueCode =
  | 'parse_error'
  | 'unauthorized_import'
  | 'import_kind_mismatch'
  | 'signature_mismatch'
  | 'memory_limits_mismatch'
  | 'table_type_mismatch'
  | 'global_type_mismatch'
  | 'duplicate_import'
  | 'unauthorized_start'
  | 'invalid_start_function';

export interface TypeExpectation {
  expected: unknown;
  actual: unknown;
}

export interface ValidationIssue {
  code: IssueCode;
  message: string;
  module?: string;
  field?: string;
  startFunctionIndex?: number;
  detail?: TypeExpectation;
}

export interface ValidationResult {
  ok: boolean;
  issues: ValidationIssue[];
  module: ParsedWasmModule | null;
}
