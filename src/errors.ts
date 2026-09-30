/** 运行期计划错误：携带来源步骤与具体路径。 */
export class PlanError extends Error {
  stepId: string;
  path?: string;
  constructor(message: string, stepId: string, path?: string) {
    super(message);
    this.name = 'PlanError';
    this.stepId = stepId;
    this.path = path;
  }
}

/** 静态诊断：错误/警告/信息，均可定位到步骤与具体路径。 */
export interface Diagnostic {
  severity: 'error' | 'warning' | 'info';
  message: string;
  stepId: string;
  path?: string;
}
