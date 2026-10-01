/** Error raised while parsing or rendering a template. Always locatable. */
export class TemplateError extends Error {
  templatePath: string | undefined;
  inputPath: string | undefined;
  constructor(message: string, templatePath?: string, inputPath?: string) {
    super(message);
    this.name = "TemplateError";
    this.templatePath = templatePath;
    this.inputPath = inputPath;
  }
}

/** Error raised by a data-store operation (set/insert/remove/move). */
export class DataError extends Error {
  path: string;
  constructor(message: string, path: string) {
    super(message);
    this.name = "DataError";
    this.path = path;
  }
}
