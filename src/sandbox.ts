/**
 * 沙箱文档的来源继承。
 *
 * 浏览器中：
 * - 普通文档的来源是其 URL 对应的元组来源；
 * - 带 sandbox 属性且未声明 allow-same-origin 的浏览上下文被赋予一个
 *   唯一的不透明来源，同一沙箱内通过 about:blank / 同源继承导航产生的
 *   文档共享该不透明身份，跨沙箱的身份则各自独立。
 */

import {
  cloneOrigin,
  opaqueOrigin,
  tupleOrigin,
  type Origin,
  type TupleOrigin,
} from "./origin.ts";

/** 模拟一个可被继承的浏览上下文/文档节点。 */
export interface SandboxDocument {
  readonly url: string;
  readonly origin: Origin;
  /** 打开者/父文档；继承型导航（about:blank 等）从这里取来源。 */
  readonly opener?: SandboxDocument;
  readonly sandboxed: boolean;
}

export interface OpenDocumentOptions {
  /** 目标 URL，about:blank 走继承路径。 */
  url: string;
  /** 是否以沙箱方式打开（无 allow-same-origin）。 */
  sandboxed: boolean;
  /** 触发打开的父文档/打开者。 */
  opener?: SandboxDocument;
}

function originFromUrl(url: string): TupleOrigin {
  const parsed = new URL(url);
  if (parsed.port !== "") {
    return tupleOrigin(parsed.protocol.replace(/:$/, ""), parsed.hostname, Number(parsed.port));
  }
  return tupleOrigin(parsed.protocol.replace(/:$/, ""), parsed.hostname);
}

/**
 * 打开一份新文档并确定其来源。
 *
 * - 沙箱文档：获得新建的不透明身份；about:blank 等继承型文档则原样继承
 *   打开者的来源对象（同一身份），保证同一沙箱内部同源、与外部不同源。
 * - 非沙箱文档：由 URL 计算元组来源；about:blank 继承打开者的结构副本。
 */
export function openDocument(options: OpenDocumentOptions): SandboxDocument {
  const { url, sandboxed, opener } = options;
  const inherits = url === "about:blank";

  let origin: Origin;
  if (sandboxed) {
    if (inherits && opener !== undefined) {
      origin = cloneOrigin(opener.origin);
    } else {
      origin = opaqueOrigin();
    }
  } else if (inherits && opener !== undefined) {
    origin = cloneOrigin(opener.origin);
  } else {
    origin = originFromUrl(url);
  }

  return Object.freeze({ url, origin, opener, sandboxed });
}
