export { parseWasmModule } from './src/parser.ts';
export { validateWasmImports, assertWasmAuthorized } from './src/validator.ts';
export { WasmParseError } from './src/binary.ts';
export type {
  CapabilityTable,
  FuncType,
  GlobalType,
  ImportCapability,
  Limits,
  MemoryCapability,
  ParsedWasmModule,
  TableType,
  ValidationIssue,
  ValidationResult,
  ValueType,
  WasmImport,
} from './src/types.ts';
