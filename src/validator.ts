/**
 * 静态安全策略校验器。
 * 在查询执行前完成：片段循环检测、展开后深度/别名倍数/加权复杂度上界计算、
 * 最坏情况字段级授权、内省阻断、变量类型匹配、敏感租户参数保护。
 */

import { Parser, ParseError } from './parser.ts';
import type {
  ComplexityBounds,
  DirectiveNode,
  DocumentNode,
  FieldDefinitionInput,
  FieldNode,
  FragmentDefinitionNode,
  FragmentSpreadNode,
  OperationDefinitionNode,
  SchemaInput,
  SecurityPolicyInput,
  SelectionSetNode,
  TypeNode,
  ValidationResult,
  ValueNode,
  Violation,
  ViolationCode,
} from './types.ts';
import {
  isScalarLiteralKind,
  isVariableAssignable,
  parseTypeRef,
  refIsList,
  refIsNonNull,
  refNamedName,
  typeNodeToString,
} from './typeRef.ts';
import type { ParsedTypeRef } from './typeRef.ts';

interface LocalViolation {
  code: ViolationCode;
  message: string;
  relativePath: string;
  fragmentChain: string[];
}

interface FragmentStats {
  relativeDepth: number;
  fieldInvocations: number;
  aliasedFields: number;
  complexity: number;
  violations: LocalViolation[];
}

interface WalkStats {
  depth: number;
  fieldInvocations: number;
  aliasedFields: number;
  complexity: number;
  violations: LocalViolation[];
}

interface VariableInfo {
  type: TypeNode;
  hasDefault: boolean;
}

const INTROSPECTION_FIELDS = new Set(['__schema', '__type']);
const META_FIELDS = new Set(['__schema', '__type', '__typename']);
const CONDITIONAL_DIRECTIVES = new Set(['include', 'skip']);

export class SecurityValidator {
  private readonly maxListSize: number;
  private readonly listFactorArgs: string[];
  private readonly blockIntrospection: boolean;
  private readonly defaultAllow: boolean;
  private readonly maxParseNesting: number;
  private readonly fragmentMemo = new Map<string, FragmentStats>();
  private readonly schema: SchemaInput;
  private readonly policy: SecurityPolicyInput;

  constructor(schema: SchemaInput, policy: SecurityPolicyInput) {
    this.schema = schema;
    this.policy = policy;
    this.maxListSize = policy.maxListSize ?? 100;
    this.listFactorArgs = policy.listFactorArgs ?? ['first', 'limit'];
    this.blockIntrospection = policy.blockIntrospection ?? true;
    this.defaultAllow = policy.defaultAllow ?? false;
    this.maxParseNesting = policy.maxParseNesting ?? 200;
  }

  validate(source: string): ValidationResult {
    let document: DocumentNode;
    try {
      document = Parser.parse(source, this.maxParseNesting);
    } catch (error) {
      if (error instanceof ParseError) {
        return this.result([
          {
            code: 'SYNTAX_ERROR',
            message: error.message,
            path: '',
            fragmentChain: [],
          },
        ]);
      }
      throw error;
    }

    const operations = document.definitions.filter(
      (definition): definition is OperationDefinitionNode =>
        definition.kind === 'OperationDefinition',
    );
    const fragments = new Map<string, FragmentDefinitionNode>();
    const violations: LocalViolation[] = [];

    for (const definition of document.definitions) {
      if (definition.kind === 'FragmentDefinition') {
        if (fragments.has(definition.name)) {
          violations.push({
            code: 'DUPLICATE_FRAGMENT',
            message: `Fragment "${definition.name}" is defined more than once`,
            relativePath: `fragment ${definition.name}`,
            fragmentChain: [],
          });
        }
        fragments.set(definition.name, definition);
      }
    }

    violations.push(...this.detectFragmentCycles(fragments));
    if (violations.some((violation) => violation.code === 'FRAGMENT_CYCLE')) {
      return this.result(violations);
    }

    const bounds: ComplexityBounds = {
      maxDepth: 0,
      aliasMultiplier: 0,
      aliasedFieldCount: 0,
      complexityUpperBound: 0,
    };

    for (const operation of operations) {
      const rootType = this.rootTypeName(operation.operation);
      const stats = this.analyzeOperation(operation, rootType, fragments);
      violations.push(...stats.violations);
      bounds.maxDepth = Math.max(bounds.maxDepth, stats.depth);
      bounds.aliasMultiplier += stats.fieldInvocations;
      bounds.aliasedFieldCount += stats.aliasedFields;
      bounds.complexityUpperBound += stats.complexity;
    }

    this.appendLimitViolations(violations, bounds);
    return this.result(violations, bounds);
  }

  private rootTypeName(operation: OperationDefinitionNode['operation']): string {
    switch (operation) {
      case 'mutation':
        return this.schema.mutationType ?? 'Mutation';
      case 'subscription':
        return this.schema.subscriptionType ?? 'Subscription';
      case 'query':
      default:
        return this.schema.queryType ?? 'Query';
    }
  }

  private detectFragmentCycles(
    fragments: Map<string, FragmentDefinitionNode>,
  ): LocalViolation[] {
    const violations: LocalViolation[] = [];
    const state = new Map<string, 'visiting' | 'done'>();

    const visit = (name: string, stack: string[]): void => {
      const mark = state.get(name);
      if (mark === 'done') {
        return;
      }
      if (mark === 'visiting') {
        const cycleStart = stack.indexOf(name);
        const cycle = [...stack.slice(cycleStart), name];
        violations.push({
          code: 'FRAGMENT_CYCLE',
          message: `Cyclic fragment reference detected: ${cycle.join(' -> ')}`,
          relativePath: `fragment ${name}`,
          fragmentChain: cycle,
        });
        return;
      }
      const fragment = fragments.get(name);
      if (!fragment) {
        violations.push({
          code: 'UNKNOWN_FRAGMENT',
          message: `Fragment "${name}" is referenced but never defined`,
          relativePath: `...${name}`,
          fragmentChain: [...stack],
        });
        return;
      }
      state.set(name, 'visiting');
      for (const spread of this.collectSpreads(fragment.selectionSet)) {
        visit(spread, [...stack, name]);
      }
      state.set(name, 'done');
    };

    for (const name of fragments.keys()) {
      visit(name, []);
    }
    return violations;
  }

  private collectSpreads(selectionSet: SelectionSetNode): string[] {
    const names: string[] = [];
    for (const selection of selectionSet.selections) {
      if (selection.kind === 'FragmentSpread') {
        names.push(selection.name);
      } else if (selection.kind === 'InlineFragment') {
        names.push(...this.collectSpreads(selection.selectionSet));
      } else if (selection.selectionSet) {
        names.push(...this.collectSpreads(selection.selectionSet));
      }
    }
    return names;
  }

  private analyzeOperation(
    operation: OperationDefinitionNode,
    rootType: string,
    fragments: Map<string, FragmentDefinitionNode>,
  ): WalkStats {
    const variables = new Map<string, VariableInfo>();
    for (const definition of operation.variableDefinitions) {
      variables.set(definition.variable, {
        type: definition.type,
        hasDefault: definition.defaultValue !== undefined,
      });
    }

    const variableViolations = this.validateVariableReferences(
      operation,
      variables,
      rootType,
    );

    this.fragmentMemo.clear();
    const walk = this.walkSelectionSet(
      operation.selectionSet,
      rootType,
      rootType,
      [],
      variables,
      fragments,
    );
    walk.violations.unshift(...variableViolations);
    return walk;
  }

  private walkSelectionSet(
    selectionSet: SelectionSetNode,
    parentType: string,
    parentPath: string,
    fragmentChain: string[],
    variables: Map<string, VariableInfo>,
    fragments: Map<string, FragmentDefinitionNode>,
  ): WalkStats {
    const stats: WalkStats = {
      depth: 0,
      fieldInvocations: 0,
      aliasedFields: 0,
      complexity: 0,
      violations: [],
    };

    for (const selection of selectionSet.selections) {
      if (selection.kind === 'Field') {
        const child = this.walkField(
          selection,
          parentType,
          parentPath,
          fragmentChain,
          variables,
          fragments,
        );
        this.mergeStats(stats, child);
      } else if (selection.kind === 'FragmentSpread') {
        const child = this.walkFragmentSpread(
          selection,
          parentType,
          parentPath,
          fragmentChain,
          variables,
          fragments,
        );
        this.mergeStats(stats, child);
      } else {
        const appliedType = selection.typeCondition
          ? this.resolveTypeCondition(selection.typeCondition, parentType)
          : parentType;
        const child = this.walkSelectionSet(
          selection.selectionSet,
          appliedType,
          parentPath,
          fragmentChain,
          variables,
          fragments,
        );
        this.mergeStats(stats, child);
      }
    }
    return stats;
  }

  private mergeStats(target: WalkStats, source: WalkStats): void {
    target.depth = Math.max(target.depth, source.depth);
    target.fieldInvocations += source.fieldInvocations;
    target.aliasedFields += source.aliasedFields;
    target.complexity += source.complexity;
    target.violations.push(...source.violations);
  }

  private resolveTypeCondition(typeCondition: string, parentType: string): string {
    if (!this.schema.types[typeCondition]) {
      return parentType;
    }
    return typeCondition;
  }

  private pathJoin(parentPath: string, fieldName: string): string {
    return parentPath === '' ? fieldName : `${parentPath}.${fieldName}`;
  }

  private walkField(
    field: FieldNode,
    parentType: string,
    parentPath: string,
    fragmentChain: string[],
    variables: Map<string, VariableInfo>,
    fragments: Map<string, FragmentDefinitionNode>,
  ): WalkStats {
    const path = this.pathJoin(parentPath, field.name);
    const parentDefinition = this.schema.types[parentType];
    const fieldDefinition =
      parentDefinition && parentDefinition.fields
        ? parentDefinition.fields[field.name]
        : undefined;
    let listFactor = 1;
    let weight = fieldDefinition?.weight ?? 1;
    const stats: WalkStats = {
      depth: 1,
      fieldInvocations: listFactor,
      aliasedFields: field.alias ? 1 : 0,
      complexity: 0,
      violations: [],
    };
    if (fieldDefinition) {
      const parsedReturn = parseTypeRef(fieldDefinition.type);
      if (refIsList(parsedReturn)) {
        listFactor = this.computeListFactor(field, path, fragmentChain, stats);
      }
    }
    stats.fieldInvocations = listFactor;

    if (INTROSPECTION_FIELDS.has(field.name) && this.blockIntrospection) {
      stats.violations.push({
        code: 'INTROSPECTION_BLOCKED',
        message: `Introspection field "${field.name}" is blocked by security policy`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
    }

    if (!fieldDefinition && !META_FIELDS.has(field.name)) {
      stats.violations.push({
        code: 'UNKNOWN_FIELD',
        message: `Field "${field.name}" does not exist on type "${parentType}"`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
    }

    if (!META_FIELDS.has(field.name)) {
      this.checkAuthorization(field.name, parentType, path, fragmentChain, stats);
      if (fieldDefinition) {
        this.checkArguments(field, fieldDefinition, path, fragmentChain, stats, variables);
      }
    }

    stats.complexity += weight * listFactor;

    if (field.selectionSet && fieldDefinition) {
      const childType = refNamedName(parseTypeRef(fieldDefinition.type));
      const child = this.walkSelectionSet(
        field.selectionSet,
        childType,
        path,
        fragmentChain,
        variables,
        fragments,
      );
      stats.depth += child.depth;
      stats.fieldInvocations += child.fieldInvocations * listFactor;
      stats.aliasedFields += child.aliasedFields;
      stats.complexity += child.complexity * listFactor;
      stats.violations.push(...child.violations);
    }

    return stats;
  }

  private walkFragmentSpread(
    spread: FragmentSpreadNode,
    parentType: string,
    parentPath: string,
    fragmentChain: string[],
    variables: Map<string, VariableInfo>,
    fragments: Map<string, FragmentDefinitionNode>,
  ): WalkStats {
    const fragment = fragments.get(spread.name);
    if (!fragment) {
      return {
        depth: 0,
        fieldInvocations: 0,
        aliasedFields: 0,
        complexity: 0,
        violations: [
          {
            code: 'UNKNOWN_FRAGMENT',
            message: `Fragment "${spread.name}" is referenced but never defined`,
            relativePath: this.pathJoin(parentPath, `...${spread.name}`),
            fragmentChain: [...fragmentChain],
          },
        ],
      };
    }

    const chain = [...fragmentChain, spread.name];
    const memo = this.analyzeFragment(fragment, variables, fragments);
    const violations: LocalViolation[] = memo.violations.map((violation) => ({
      code: violation.code,
      message: violation.message,
      relativePath: this.canonicalizeRelativePath(
        parentPath,
        violation.relativePath,
      ),
      fragmentChain: this.canonicalizeChain(fragmentChain, violation.fragmentChain),
    }));

    return {
      depth: memo.relativeDepth,
      fieldInvocations: memo.fieldInvocations,
      aliasedFields: memo.aliasedFields,
      complexity: memo.complexity,
      violations,
    };
  }

  private analyzeFragment(
    fragment: FragmentDefinitionNode,
    variables: Map<string, VariableInfo>,
    fragments: Map<string, FragmentDefinitionNode>,
  ): FragmentStats {
    const cached = this.fragmentMemo.get(fragment.name);
    if (cached) {
      return cached;
    }

    const placeholder: FragmentStats = {
      relativeDepth: 0,
      fieldInvocations: 0,
      aliasedFields: 0,
      complexity: 0,
      violations: [],
    };
    this.fragmentMemo.set(fragment.name, placeholder);

    if (!this.schema.types[fragment.typeCondition]) {
      placeholder.violations.push({
        code: 'UNKNOWN_TYPE',
        message: `Fragment "${fragment.name}" references unknown type "${fragment.typeCondition}"`,
        relativePath: `fragment ${fragment.name}`,
        fragmentChain: [fragment.name],
      });
    }

    const walk = this.walkSelectionSet(
      fragment.selectionSet,
      fragment.typeCondition,
      fragment.typeCondition,
      [fragment.name],
      variables,
      fragments,
    );
    placeholder.relativeDepth = walk.depth;
    placeholder.fieldInvocations = walk.fieldInvocations;
    placeholder.aliasedFields = walk.aliasedFields;
    placeholder.complexity = walk.complexity;
    placeholder.violations.push(...walk.violations);
    return placeholder;
  }

  private canonicalizeRelativePath(parentPath: string, relativePath: string): string {
    if (relativePath.startsWith('fragment ')) {
      return parentPath === '' ? relativePath : `${parentPath}.${relativePath}`;
    }
    const firstDot = relativePath.indexOf('.');
    const withoutTypePrefix =
      firstDot === -1 ? relativePath : relativePath.slice(firstDot + 1);
    return parentPath === ''
      ? relativePath
      : `${parentPath}.${withoutTypePrefix}`;
  }

  private canonicalizeChain(
    outerChain: string[],
    innerChain: string[],
  ): string[] {
    return [...outerChain, ...innerChain];
  }

  private checkAuthorization(
    fieldName: string,
    parentType: string,
    path: string,
    fragmentChain: string[],
    stats: WalkStats,
  ): void {
    const key = `${parentType}.${fieldName}`;
    const rolePolicy = this.policy.policies?.[this.policy.role];
    let decision: boolean | undefined = rolePolicy?.fields?.[key];
    if (decision === undefined) {
      decision = rolePolicy?.defaultAllow ?? this.defaultAllow;
    }
    if (!decision) {
      stats.violations.push({
        code: 'FIELD_FORBIDDEN',
        message: `Role "${this.policy.role}" is not allowed to access ${key}`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
    }
  }

  private checkArguments(
    field: FieldNode,
    fieldDefinition: FieldDefinitionInput,
    path: string,
    fragmentChain: string[],
    stats: WalkStats,
    variables: Map<string, VariableInfo>,
  ): void {
    for (const argument of field.args) {
      const argumentDefinition = fieldDefinition.args?.[argument.name];
      if (!argumentDefinition) {
        continue;
      }
      if (argumentDefinition.sensitive) {
        stats.violations.push({
          code: 'SENSITIVE_ARG_OVERRIDE',
          message: `Sensitive tenant-scoped argument "${argument.name}" at ${path} must not be supplied by the client`,
          relativePath: this.pathJoin(path, argument.name),
          fragmentChain: [...fragmentChain],
        });
      }
      this.checkValueType(
        argument.value,
        parseTypeRef(argumentDefinition.type),
        this.pathJoin(path, argument.name),
        fragmentChain,
        stats,
        variables,
      );
    }
  }

  private checkValueType(
    value: ValueNode,
    expected: ParsedTypeRef,
    path: string,
    fragmentChain: string[],
    stats: WalkStats,
    variables: Map<string, VariableInfo>,
  ): void {
    if (value.kind === 'Variable') {
      this.checkVariableUsage(value.name, expected, path, fragmentChain, stats, variables);
      return;
    }

    if (value.kind === 'NullValue') {
      if (refIsNonNull(expected)) {
        stats.violations.push({
          code: 'VARIABLE_TYPE_MISMATCH',
          message: `Null value is not assignable to non-null type at ${path}`,
          relativePath: path,
          fragmentChain: [...fragmentChain],
        });
      }
      return;
    }

    const nullable = expected.kind === 'NonNull' ? expected.inner : expected;

    if (nullable.kind === 'List') {
      if (value.kind !== 'ListValue') {
        stats.violations.push({
          code: 'VARIABLE_TYPE_MISMATCH',
          message: `List value expected at ${path} but found ${value.kind}`,
          relativePath: path,
          fragmentChain: [...fragmentChain],
        });
        return;
      }
      value.values.forEach((item, index) => {
        this.checkValueType(
          item,
          nullable.kind === 'List' ? nullable.inner : nullable,
          `${path}[${index}]`,
          fragmentChain,
          stats,
          variables,
        );
      });
      return;
    }

    const namedType = this.schema.types[nullable.name];
    if (namedType?.kind === 'INPUT_OBJECT') {
      if (value.kind !== 'ObjectValue') {
        stats.violations.push({
          code: 'VARIABLE_TYPE_MISMATCH',
          message: `Input object "${nullable.name}" expected at ${path} but found ${value.kind}`,
          relativePath: path,
          fragmentChain: [...fragmentChain],
        });
        return;
      }
      for (const objectField of value.fields) {
        const inputField = namedType.inputFields?.[objectField.name];
        if (!inputField) {
          continue;
        }
        if (inputField.sensitive) {
          stats.violations.push({
            code: 'SENSITIVE_ARG_OVERRIDE',
            message: `Sensitive tenant-scoped input field "${objectField.name}" at ${path} must not be supplied by the client`,
            relativePath: this.pathJoin(path, objectField.name),
            fragmentChain: [...fragmentChain],
          });
        }
        this.checkValueType(
          objectField.value,
          parseTypeRef(inputField.type),
          this.pathJoin(path, objectField.name),
          fragmentChain,
          stats,
          variables,
        );
      }
      return;
    }

    if (namedType?.kind === 'ENUM') {
      if (
        value.kind !== 'EnumValue' ||
        !namedType.enumValues?.includes(value.value)
      ) {
        stats.violations.push({
          code: 'VARIABLE_TYPE_MISMATCH',
          message: `Value at ${path} is not a valid "${nullable.name}" enum literal`,
          relativePath: path,
          fragmentChain: [...fragmentChain],
        });
      }
      return;
    }

    if (!this.literalMatchesScalar(value, nullable.name)) {
      stats.violations.push({
        code: 'VARIABLE_TYPE_MISMATCH',
        message: `Value at ${path} is not assignable to scalar "${nullable.name}"`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
    }
  }

  private literalMatchesScalar(value: ValueNode, scalarName: string): boolean {
    if (!isScalarLiteralKind(value.kind)) {
      return false;
    }
    switch (scalarName) {
      case 'Int':
        return value.kind === 'IntValue';
      case 'Float':
        return value.kind === 'IntValue' || value.kind === 'FloatValue';
      case 'String':
      case 'ID':
        return value.kind === 'StringValue' || value.kind === 'EnumValue';
      case 'Boolean':
        return value.kind === 'BooleanValue';
      default:
        return true;
    }
  }

  private checkVariableUsage(
    variableName: string,
    expected: ParsedTypeRef,
    path: string,
    fragmentChain: string[],
    stats: WalkStats,
    variables: Map<string, VariableInfo>,
  ): void {
    const info = variables.get(variableName);
    if (!info) {
      stats.violations.push({
        code: 'VARIABLE_NOT_DEFINED',
        message: `Variable "$${variableName}" used at ${path} is not declared by the operation`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
      return;
    }
    if (!isVariableAssignable(info.type, expected)) {
      stats.violations.push({
        code: 'VARIABLE_TYPE_MISMATCH',
        message: `Variable "$${variableName}" of type ${typeNodeToString(info.type)} is not assignable to ${this.printParsed(expected)} at ${path}`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
      return;
    }
    if (
      refIsNonNull(expected) &&
      info.type.kind !== 'NonNullType' &&
      !info.hasDefault
    ) {
      stats.violations.push({
        code: 'VARIABLE_REQUIRED',
        message: `Nullable variable "$${variableName}" cannot satisfy required argument at ${path}`,
        relativePath: path,
        fragmentChain: [...fragmentChain],
      });
    }
  }

  private printParsed(ref: ParsedTypeRef): string {
    switch (ref.kind) {
      case 'Named':
        return ref.name;
      case 'List':
        return `[${this.printParsed(ref.inner)}]`;
      case 'NonNull':
        return `${this.printParsed(ref.inner)}!`;
    }
  }

  private computeListFactor(
    field: FieldNode,
    path: string,
    fragmentChain: string[],
    stats: WalkStats,
  ): number {
    let factor = 1;
    for (const argument of field.args) {
      if (!this.listFactorArgs.includes(argument.name)) {
        continue;
      }
      const value = argument.value;
      if (value.kind === 'Variable') {
        factor *= this.maxListSize;
      } else if (value.kind === 'IntValue') {
        const parsed = Number(value.value);
        if (!Number.isSafeInteger(parsed) || parsed < 0) {
          stats.violations.push({
            code: 'INVALID_LIST_ARGUMENT',
            message: `List argument "${argument.name}" at ${path} must be a non-negative safe integer`,
            relativePath: this.pathJoin(path, argument.name),
            fragmentChain: [...fragmentChain],
          });
          factor *= this.maxListSize;
        } else {
          factor *= Math.min(parsed, this.maxListSize);
        }
      } else {
        stats.violations.push({
          code: 'INVALID_LIST_ARGUMENT',
          message: `List argument "${argument.name}" at ${path} must be an integer literal or variable`,
          relativePath: this.pathJoin(path, argument.name),
          fragmentChain: [...fragmentChain],
        });
      }
    }
    return factor;
  }

  private validateVariableReferences(
    operation: OperationDefinitionNode,
    variables: Map<string, VariableInfo>,
    rootType: string,
  ): LocalViolation[] {
    const violations: LocalViolation[] = [];
    const target: WalkStats = {
      depth: 0,
      fieldInvocations: 0,
      aliasedFields: 0,
      complexity: 0,
      violations,
    };

    const checkDirectives = (directives: DirectiveNode[], path: string): void => {
      for (const directive of directives) {
        if (!CONDITIONAL_DIRECTIVES.has(directive.name)) {
          continue;
        }
        const condition = directive.args.find((argument) => argument.name === 'if');
        if (!condition) {
          violations.push({
            code: 'VARIABLE_TYPE_MISMATCH',
            message: `Directive "@${directive.name}" requires a Boolean "if" argument at ${path}`,
            relativePath: path,
            fragmentChain: [],
          });
          continue;
        }
        if (condition.value.kind === 'BooleanValue') {
          continue;
        }
        if (condition.value.kind !== 'Variable') {
          violations.push({
            code: 'VARIABLE_TYPE_MISMATCH',
            message: `Directive "@${directive.name}" argument "if" must be a Boolean variable or literal at ${path}`,
            relativePath: path,
            fragmentChain: [],
          });
          continue;
        }
        this.checkVariableUsage(
          condition.value.name,
          parseTypeRef('Boolean!'),
          path,
          [],
          target,
          variables,
        );
      }
    };

    const walkSelectionSet = (selectionSet: SelectionSetNode, path: string): void => {
      for (const selection of selectionSet.selections) {
        if (selection.kind === 'Field') {
          checkDirectives(selection.directives, this.pathJoin(path, selection.name));
          if (selection.selectionSet) {
            walkSelectionSet(selection.selectionSet, this.pathJoin(path, selection.name));
          }
        } else if (selection.kind === 'InlineFragment') {
          checkDirectives(selection.directives, this.pathJoin(path, '...on'));
          walkSelectionSet(selection.selectionSet, path);
        } else {
          checkDirectives(selection.directives, this.pathJoin(path, `...${selection.name}`));
        }
      }
    };

    checkDirectives(operation.directives, operation.operation);
    walkSelectionSet(operation.selectionSet, rootType);
    return violations;
  }

  private appendLimitViolations(
    violations: LocalViolation[],
    bounds: ComplexityBounds,
  ): void {
    if (this.policy.maxDepth !== undefined && bounds.maxDepth > this.policy.maxDepth) {
      violations.push({
        code: 'LIMIT_DEPTH_EXCEEDED',
        message: `Expanded max depth ${bounds.maxDepth} exceeds limit ${this.policy.maxDepth}`,
        relativePath: '',
        fragmentChain: [],
      });
    }
    if (
      this.policy.maxAliasMultiplier !== undefined &&
      bounds.aliasMultiplier > this.policy.maxAliasMultiplier
    ) {
      violations.push({
        code: 'LIMIT_ALIAS_EXCEEDED',
        message: `Expanded alias multiplier ${bounds.aliasMultiplier} exceeds limit ${this.policy.maxAliasMultiplier}`,
        relativePath: '',
        fragmentChain: [],
      });
    }
    if (
      this.policy.maxComplexity !== undefined &&
      bounds.complexityUpperBound > this.policy.maxComplexity
    ) {
      violations.push({
        code: 'LIMIT_COMPLEXITY_EXCEEDED',
        message: `Complexity upper bound ${bounds.complexityUpperBound} exceeds limit ${this.policy.maxComplexity}`,
        relativePath: '',
        fragmentChain: [],
      });
    }
  }

  private result(
    violations: LocalViolation[],
    bounds?: ComplexityBounds,
  ): ValidationResult {
    const finalBounds: ComplexityBounds = bounds ?? {
      maxDepth: 0,
      aliasMultiplier: 0,
      aliasedFieldCount: 0,
      complexityUpperBound: 0,
    };
    const sorted = [...violations].sort((left, right) => {
      const pathCompare = left.relativePath.localeCompare(right.relativePath);
      if (pathCompare !== 0) {
        return pathCompare;
      }
      return left.code.localeCompare(right.code);
    });
    const output: Violation[] = sorted.map((violation) => ({
      code: violation.code,
      message: violation.message,
      path: violation.relativePath,
      fragmentChain: violation.fragmentChain,
      bounds:
        violation.code.startsWith('LIMIT_') || violation.code === 'FRAGMENT_CYCLE'
          ? { ...finalBounds }
          : undefined,
    }));
    return {
      ok: output.length === 0,
      violations: output,
      ...finalBounds,
    };
  }
}
