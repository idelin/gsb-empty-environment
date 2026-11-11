/** 模板引擎的所有失败都通过 TemplateError 体系抛出，绝不泄漏宿主异常。 */

export type TemplateErrorCode =
  | 'INVALID_DATA'
  | 'UNKNOWN_NAME'
  | 'UNKNOWN_FIELD'
  | 'TYPE_MISMATCH'
  | 'NOT_A_MACRO'
  | 'ARITY_MISMATCH'
  | 'DUPLICATE_MACRO'
  | 'DEPTH_BUDGET_EXCEEDED'
  | 'OUTPUT_BUDGET_EXCEEDED'
  | 'ITERATION_BUDGET_EXCEEDED'
  | 'DATA_BUDGET_EXCEEDED';

export class TemplateError extends Error {
  readonly code: TemplateErrorCode;

  constructor(code: TemplateErrorCode, message: string) {
    super(message);
    this.name = 'TemplateError';
    this.code = code;
  }
}

export class BudgetExceededError extends TemplateError {
  constructor(
    code: Extract<
      TemplateErrorCode,
      | 'DEPTH_BUDGET_EXCEEDED'
      | 'OUTPUT_BUDGET_EXCEEDED'
      | 'ITERATION_BUDGET_EXCEEDED'
      | 'DATA_BUDGET_EXCEEDED'
    >,
    message: string,
  ) {
    super(code, message);
    this.name = 'BudgetExceededError';
  }
}
