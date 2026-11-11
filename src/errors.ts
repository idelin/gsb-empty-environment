export class QueryStructureError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'QueryStructureError';
  }
}
