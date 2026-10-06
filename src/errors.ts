export class UnknownVersionError extends Error {
  readonly versionId: string;

  constructor(versionId: string) {
    super(`Unknown version: "${versionId}"`);
    this.name = 'UnknownVersionError';
    this.versionId = versionId;
  }
}

export class UnknownBranchError extends Error {
  readonly branch: string;

  constructor(branch: string) {
    super(`Unknown branch: "${branch}"`);
    this.name = 'UnknownBranchError';
    this.branch = branch;
  }
}

export class BranchExistsError extends Error {
  readonly branch: string;

  constructor(branch: string) {
    super(`Branch already exists: "${branch}"`);
    this.name = 'BranchExistsError';
    this.branch = branch;
  }
}

export class DetachedHeadError extends Error {
  readonly versionId: string;

  constructor(versionId: string) {
    super(
      `Cannot commit from historical version "${versionId}". ` +
        `Editing the past would overwrite a branch tip; call view.fork(name) ` +
        `to explicitly create a branch first.`,
    );
    this.name = 'DetachedHeadError';
    this.versionId = versionId;
  }
}

export class ViewClosedError extends Error {
  constructor() {
    super('This view session has been closed.');
    this.name = 'ViewClosedError';
  }
}
