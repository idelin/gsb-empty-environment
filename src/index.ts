export {
  XIncludeProcessor,
  XINCLUDE_NAMESPACE,
  type XIncludeOptions,
  type ProcessResult,
} from "./xinclude-processor.ts";
export {
  MapResourceResolver,
  type ResourceResolver,
  type ResolveContext,
} from "./resource-resolver.ts";
export { normalizeHref } from "./href.ts";
export { parseXml } from "./xml-parser.ts";
export { serializeXml } from "./xml-serializer.ts";
export {
  XIncludeError,
  XIncludeSecurityError,
  XIncludeResourceError,
  XIncludeCycleError,
  XIncludeLimitError,
  XmlParseError,
} from "./errors.ts";
