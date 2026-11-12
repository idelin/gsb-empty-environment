export class InvalidLastEventIdError extends Error {
  readonly code: "INVALID_LAST_EVENT_ID";
  readonly statusCode: 400;

  constructor() {
    super("Invalid Last-Event-ID");
    this.name = "InvalidLastEventIdError";
    this.code = "INVALID_LAST_EVENT_ID";
    this.statusCode = 400;
  }
}
