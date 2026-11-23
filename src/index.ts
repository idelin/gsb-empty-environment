export {
  encodeAttr,
  encodeComment,
  encodeCss,
  encodeHtml,
  encodeJs,
  encodeUrl,
  decodeEntities,
  escape,
  safe,
  SafeString,
  UnsafeRawOutputError,
  UnsafeUrlError,
  type EscapeContext,
} from './escaper.ts';

export { compile, render } from './engine.ts';
