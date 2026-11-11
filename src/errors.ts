export class CopyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'CopyError';
  }
}

export class ProxyRejectedError extends CopyError {
  constructor() {
    super('Proxy objects are not allowed in copied messages');
    this.name = 'ProxyRejectedError';
  }
}

export class CapabilityLeakError extends CopyError {
  constructor() {
    super('Registered capability handle must not cross the trust boundary');
    this.name = 'CapabilityLeakError';
  }
}

export class AccessorRejectedError extends CopyError {
  constructor(pathHint: string) {
    super(`Accessor properties are not allowed in copied messages (${pathHint})`);
    this.name = 'AccessorRejectedError';
  }
}

export class UnsupportedValueError extends CopyError {
  constructor(typeName: string) {
    super(`Values of type ${typeName} are not allowed in copied messages`);
    this.name = 'UnsupportedValueError';
  }
}

export class BudgetExceededError extends CopyError {
  constructor(kind: 'node' | 'byte', used: number, limit: number) {
    super(`Copy ${kind} budget exceeded: ${used} > ${limit}`);
    this.name = 'BudgetExceededError';
  }
}
