import { WasmParseError } from './binary.ts';
import { parseWasmModule } from './parser.ts';
import type {
  CapabilityTable,
  FuncType,
  GlobalType,
  ImportCapability,
  Limits,
  ParsedWasmModule,
  TableType,
  ValidationIssue,
  ValidationResult,
  ValueType,
  WasmImport,
} from './types.ts';

export function validateWasmImports(
  bytes: Uint8Array,
  capabilities: CapabilityTable,
): ValidationResult {
  let module: ParsedWasmModule;
  try {
    module = parseWasmModule(bytes);
  } catch (error) {
    if (error instanceof WasmParseError) {
      return {
        ok: false,
        issues: [{ code: 'parse_error', message: error.message }],
        module: null,
      };
    }
    throw error;
  }

  const issues: ValidationIssue[] = [];
  validateImports(module, capabilities, issues);
  validateStart(module, capabilities, issues);

  return { ok: issues.length === 0, issues, module };
}

export function assertWasmAuthorized(
  bytes: Uint8Array,
  capabilities: CapabilityTable,
): ParsedWasmModule {
  const result = validateWasmImports(bytes, capabilities);
  if (!result.ok) {
    const summary = result.issues.map((issue) => issue.message).join('; ');
    throw new WasmParseError(`wasm import authorization failed: ${summary}`);
  }
  return result.module as ParsedWasmModule;
}

function validateImports(
  module: ParsedWasmModule,
  capabilities: CapabilityTable,
  issues: ValidationIssue[],
): void {
  const seenImports = new Set<string>();

  for (const importEntry of module.imports) {
    const identity = `${importEntry.module}\u0000${importEntry.field}`;
    if (seenImports.has(identity)) {
      issues.push({
        code: 'duplicate_import',
        message: `duplicate import of ${importEntry.module}.${importEntry.field}`,
        module: importEntry.module,
        field: importEntry.field,
      });
    }
    seenImports.add(identity);

    const granted = capabilities.modules[importEntry.module]?.fields[importEntry.field];
    if (!granted) {
      issues.push({
        code: 'unauthorized_import',
        message: `import ${importEntry.module}.${importEntry.field} is not granted by the capability table`,
        module: importEntry.module,
        field: importEntry.field,
      });
      continue;
    }

    validateImportedKind(importEntry, granted, module, issues);
  }
}

function validateImportedKind(
  importEntry: WasmImport,
  granted: ImportCapability,
  module: ParsedWasmModule,
  issues: ValidationIssue[],
): void {
  if (importEntry.kind !== granted.kind) {
    issues.push({
      code: 'import_kind_mismatch',
      message:
        `import ${importEntry.module}.${importEntry.field} declares ${importEntry.kind} ` +
        `but the capability grants ${granted.kind}`,
      module: importEntry.module,
      field: importEntry.field,
      detail: { expected: granted.kind, actual: importEntry.kind },
    });
    return;
  }

  if (importEntry.kind === 'func' && granted.kind === 'func') {
    const actualType = module.types[importEntry.typeIndex];
    if (!functionTypesEqual(actualType, granted.type)) {
      issues.push({
        code: 'signature_mismatch',
        message:
          `function import ${importEntry.module}.${importEntry.field} ` +
          `declares ${formatFuncType(actualType)} but capability requires ${formatFuncType(granted.type)}`,
        module: importEntry.module,
        field: importEntry.field,
        detail: { expected: granted.type, actual: actualType },
      });
    }
    return;
  }

  if (importEntry.kind === 'memory' && granted.kind === 'memory') {
    if (!limitsEqual(importEntry.limits, granted.limits)) {
      issues.push({
        code: 'memory_limits_mismatch',
        message:
          `memory import ${importEntry.module}.${importEntry.field} ` +
          `declares ${formatLimits(importEntry.limits)} but capability requires ${formatLimits(granted.limits)}`,
        module: importEntry.module,
        field: importEntry.field,
        detail: { expected: granted.limits, actual: importEntry.limits },
      });
    }
    return;
  }

  if (importEntry.kind === 'table' && granted.kind === 'table') {
    if (!tableTypesEqual(importEntry.tableType, granted.tableType)) {
      issues.push({
        code: 'table_type_mismatch',
        message:
          `table import ${importEntry.module}.${importEntry.field} ` +
          `declares ${formatTableType(importEntry.tableType)} but capability requires ${formatTableType(granted.tableType)}`,
        module: importEntry.module,
        field: importEntry.field,
        detail: { expected: granted.tableType, actual: importEntry.tableType },
      });
    }
    return;
  }

  if (importEntry.kind === 'global' && granted.kind === 'global') {
    if (!globalTypesEqual(importEntry.globalType, granted.globalType)) {
      issues.push({
        code: 'global_type_mismatch',
        message:
          `global import ${importEntry.module}.${importEntry.field} ` +
          `declares ${formatGlobalType(importEntry.globalType)} but capability requires ${formatGlobalType(granted.globalType)}`,
        module: importEntry.module,
        field: importEntry.field,
        detail: { expected: granted.globalType, actual: importEntry.globalType },
      });
    }
  }
}

function validateStart(
  module: ParsedWasmModule,
  capabilities: CapabilityTable,
  issues: ValidationIssue[],
): void {
  if (module.start === null) {
    return;
  }

  const startIndex = module.start.functionIndex;
  const startType = resolveFunctionType(module, startIndex);
  if (startType === null) {
    issues.push({
      code: 'invalid_start_function',
      message: `start function index ${startIndex} is outside the module function index space`,
      startFunctionIndex: startIndex,
    });
    return;
  }

  if (startType.params.length !== 0 || startType.results.length !== 0) {
    issues.push({
      code: 'invalid_start_function',
      message: `start function index ${startIndex} must have empty parameters and results, got ${formatFuncType(startType)}`,
      startFunctionIndex: startIndex,
      detail: { expected: { params: [], results: [] }, actual: startType },
    });
    return;
  }

  if (capabilities.allowStart !== true) {
    issues.push({
      code: 'unauthorized_start',
      message:
        `module declares start function ${startIndex}, but start execution is not authorized ` +
        `for this capability tier`,
      startFunctionIndex: startIndex,
    });
  }
}

function resolveFunctionType(module: ParsedWasmModule, functionIndex: number): FuncType | null {
  const importedFunc = module.imports.find(
    (entry) => entry.kind === 'func' && entry.functionIndex === functionIndex,
  );
  if (importedFunc && importedFunc.kind === 'func') {
    return module.types[importedFunc.typeIndex];
  }

  const definedFunc = module.functions.find((entry) => entry.index === functionIndex);
  if (definedFunc) {
    return module.types[definedFunc.typeIndex];
  }
  return null;
}

function functionTypesEqual(actual: FuncType, expected: FuncType): boolean {
  return (
    valueTypesEqual(actual.params, expected.params) &&
    valueTypesEqual(actual.results, expected.results)
  );
}

function valueTypesEqual(actual: ValueType[], expected: ValueType[]): boolean {
  if (actual.length !== expected.length) {
    return false;
  }
  return actual.every((valueType, index) => valueType === expected[index]);
}

function limitsEqual(actual: Limits, expected: Limits): boolean {
  return (
    actual.min === expected.min &&
    actual.max === expected.max &&
    actual.shared === expected.shared
  );
}

function tableTypesEqual(actual: TableType, expected: TableType): boolean {
  return (
    actual.elementType === expected.elementType && limitsEqual(actual.limits, expected.limits)
  );
}

function globalTypesEqual(actual: GlobalType, expected: GlobalType): boolean {
  return actual.valueType === expected.valueType && actual.mutable === expected.mutable;
}

function formatFuncType(type: FuncType): string {
  return `(${type.params.join(', ')}) -> (${type.results.join(', ')})`;
}

function formatLimits(limits: Limits): string {
  const maximum = limits.max === null ? 'no maximum' : `max=${limits.max}`;
  return `{min=${limits.min}, ${maximum}, shared=${limits.shared}}`;
}

function formatTableType(type: TableType): string {
  return `${type.elementType} ${formatLimits(type.limits)}`;
}

function formatGlobalType(type: GlobalType): string {
  return `${type.mutable ? 'mut ' : ''}${type.valueType}`;
}
