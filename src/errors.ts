export type IssueCode = 'dangling-ref' | 'type-mismatch' | 'duplicate-id';

export interface Issue {
  code: IssueCode;
  message: string;
  path: string;
  refId?: string;
}

export class GraphError extends Error {
  readonly path: string;
  constructor(message: string, path: string) {
    super(`${message} (at ${path})`);
    this.name = new.target.name;
    this.path = path;
  }
}

export class PathError extends GraphError {}

export class PendingRefError extends GraphError {
  readonly refId: string;
  constructor(refId: string, path: string) {
    super(`reference "${refId}" is pending: its target document is gone`, path);
    this.refId = refId;
  }
}

export class DuplicateIdError extends GraphError {
  readonly idName: string;
  constructor(idName: string, path: string) {
    super(`duplicate $id "${idName}"`, path);
    this.idName = idName;
  }
}

export class PublishError extends Error {
  readonly issues: Issue[];
  constructor(issues: Issue[]) {
    super(
      `publish rejected: ${issues.length} issue(s): ` +
        issues.map((i) => `[${i.code}] ${i.message} @ ${i.path}`).join('; '),
    );
    this.name = 'PublishError';
    this.issues = issues;
  }
}
