import {
  XIncludeCycleError,
  XIncludeError,
  XIncludeLimitError,
  XIncludeResourceError,
  XIncludeSecurityError,
} from "./errors.ts";
import { normalizeHref } from "./href.ts";
import type { ResourceResolver } from "./resource-resolver.ts";
import { serializeXml } from "./xml-serializer.ts";
import {
  parseXml,
  XML_NAMESPACE,
  type XmlAttribute,
  type XmlDocument,
  type XmlElement,
  type XmlNode,
} from "./xml-parser.ts";

export const XINCLUDE_NAMESPACE = "http://www.w3.org/2001/XInclude";

export interface XIncludeOptions {
  /** 调用方登记同包资源所用的解析器（必填） */
  readonly resolver: ResourceResolver;
  /** 根文档在包内的资源键，默认 root.xml */
  readonly rootResource?: string;
  /** 最大嵌套包含深度（根文档深度为 0），默认 16 */
  readonly maxDepth?: number;
  /** 展开后允许消耗的最大 UTF-8 字节数，默认 1 MiB */
  readonly maxExpandedBytes?: number;
}

export interface ProcessResult {
  /** 完全展开并序列化后的 XML 字符串 */
  readonly xml: string;
  /** 解析器回调被调用的总次数 */
  readonly resolverCalls: number;
  /** 处理过程中消耗的 UTF-8 字节数 */
  readonly expandedBytes: number;
}

const DEFAULT_MAX_DEPTH = 16;
const DEFAULT_MAX_BYTES = 1024 * 1024;

export class XIncludeProcessor {
  private readonly resolver: ResourceResolver;
  private readonly rootResource: string;
  private readonly maxDepth: number;
  private readonly maxExpandedBytes: number;
  private bytesUsed = 0;
  private resolverCallCount = 0;

  constructor(options: XIncludeOptions) {
    this.resolver = options.resolver;
    this.rootResource = options.rootResource ?? "root.xml";
    this.maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
    this.maxExpandedBytes = options.maxExpandedBytes ?? DEFAULT_MAX_BYTES;
    if (this.maxDepth < 0 || !Number.isInteger(this.maxDepth)) {
      throw new XIncludeError("maxDepth 必须是非负整数");
    }
    if (this.maxExpandedBytes <= 0 || !Number.isInteger(this.maxExpandedBytes)) {
      throw new XIncludeError("maxExpandedBytes 必须是正整数");
    }
  }

  async process(rootSource: string): Promise<ProcessResult> {
    this.bytesUsed = 0;
    this.resolverCallCount = 0;
    this.chargeBytes(rootSource, this.rootResource);
    const document = parseXml(rootSource);
    const expanded = await this.expandElement(
      document.root,
      this.rootResource,
      0,
      [],
    );
    return {
      xml: serializeXml(expanded),
      resolverCalls: this.resolverCallCount,
      expandedBytes: this.bytesUsed,
    };
  }

  private chargeBytes(source: string, resource: string): void {
    this.bytesUsed += Buffer.byteLength(source, "utf8");
    if (this.bytesUsed > this.maxExpandedBytes) {
      throw new XIncludeLimitError(
        `展开字节数 ${this.bytesUsed} 超过上限 ${this.maxExpandedBytes}（资源 ${resource}）`,
      );
    }
  }

  private async expandElement(
    element: XmlElement,
    resource: string,
    depth: number,
    ancestorChain: readonly string[],
  ): Promise<XmlElement> {
    if (this.isXInclude(element, "include")) {
      throw new XIncludeSecurityError(
        "xi:include 不得作为仍需展开的元素出现（内部错误）",
      );
    }

    const expandedChildren: XmlNode[] = [];
    for (const child of element.children) {
      if (child.kind === "element" && this.isXInclude(child, "include")) {
        expandedChildren.push(
          ...(await this.handleInclude(child, resource, depth, ancestorChain)),
        );
        continue;
      }
      if (child.kind === "element") {
        if (this.isXInclude(child, "fallback")) {
          // 脱离 include 上下文的 fallback 没有语义，直接剥离。
          continue;
        }
        expandedChildren.push(
          await this.expandElement(child, resource, depth, ancestorChain),
        );
        continue;
      }
      expandedChildren.push(child);
    }

    return { ...element, children: expandedChildren };
  }

  private async handleInclude(
    include: XmlElement,
    resource: string,
    depth: number,
    ancestorChain: readonly string[],
  ): Promise<XmlNode[]> {
    const parseMode = this.parseMode(include);
    const href = this.requireHref(include);
    this.rejectUnsupportedAttributes(include);
    this.validateIncludeChildren(include);

    try {
      if (depth >= this.maxDepth) {
        throw new XIncludeResourceError(
          `嵌套深度超过上限 ${this.maxDepth}（资源 ${resource}）`,
        );
      }

      // 归一化不参考 xml:base，基准目录只来自当前文档自身的包内位置。
      const key = normalizeHref(href, resource);
      if (ancestorChain.includes(key)) {
        throw new XIncludeCycleError([...ancestorChain, key]);
      }

      this.resolverCallCount += 1;
      const content = await this.resolver.resolve({
        key,
        href,
        fromResource: resource,
        depth: depth + 1,
      });
      if (content === null) {
        throw new XIncludeResourceError(`资源未登记或不可见: ${key}`);
      }
      this.chargeBytes(content, key);

      if (parseMode === "text") {
        // 文本整体作为单个文本节点，经序列化转义，无法注入 XML 标记。
        return [{ kind: "text", text: content }];
      }

      const includedDocument: XmlDocument = parseXml(content);
      const expandedIncluded = await this.expandElement(
        includedDocument.root,
        key,
        depth + 1,
        [...ancestorChain, key],
      );
      return [expandedIncluded];
    } catch (error) {
      return this.applyFallback(include, error, resource, depth, ancestorChain);
    }
  }

  private async applyFallback(
    include: XmlElement,
    error: unknown,
    resource: string,
    depth: number,
    ancestorChain: readonly string[],
  ): Promise<XmlNode[]> {
    // 安全策略错误与全局字节上限不允许被 fallback 吞掉。
    if (
      error instanceof XIncludeSecurityError ||
      error instanceof XIncludeLimitError
    ) {
      throw error;
    }

    const fallback = include.children.find(
      (child): child is XmlElement =>
        child.kind === "element" && this.isXInclude(child, "fallback"),
    );
    if (!fallback) {
      throw error;
    }

    const expandedFallback: XmlNode[] = [];
    for (const child of fallback.children) {
      if (child.kind === "element" && this.isXInclude(child, "include")) {
        expandedFallback.push(
          ...(await this.handleInclude(child, resource, depth, ancestorChain)),
        );
        continue;
      }
      if (child.kind === "element") {
        expandedFallback.push(
          await this.expandElement(child, resource, depth, ancestorChain),
        );
        continue;
      }
      expandedFallback.push(child);
    }
    return expandedFallback;
  }

  private isXInclude(element: XmlElement, localName: string): boolean {
    return (
      element.namespaceUri === XINCLUDE_NAMESPACE &&
      element.localName === localName
    );
  }

  private parseMode(include: XmlElement): "xml" | "text" {
    const parse = this.findAttribute(include, null, "parse")?.value ?? "xml";
    if (parse !== "xml" && parse !== "text") {
      throw new XIncludeSecurityError(`不支持的 parse 模式: ${parse}`);
    }
    return parse;
  }

  private requireHref(include: XmlElement): string {
    const href = this.findAttribute(include, null, "href")?.value;
    if (href === undefined) {
      throw new XIncludeSecurityError("xi:include 缺少 href 属性");
    }
    return href;
  }

  private rejectUnsupportedAttributes(include: XmlElement): void {
    for (const attribute of include.attributes) {
      if (attribute.namespaceUri === XML_NAMESPACE) {
        // xml:base/xml:lang 等不参与安全决策；xml:base 在序列化时剥离。
        continue;
      }
      const allowed =
        attribute.namespaceUri === null &&
        (attribute.localName === "href" ||
          attribute.localName === "parse" ||
          attribute.localName === "encoding");
      if (!allowed) {
        throw new XIncludeSecurityError(
          `xi:include 不支持属性 ${attribute.qname}`,
        );
      }
    }
  }

  private validateIncludeChildren(include: XmlElement): void {
    for (const child of include.children) {
      if (
        child.kind === "element" &&
        this.isXInclude(child, "fallback")
      ) {
        continue;
      }
      if (child.kind === "text" && child.text.trim() === "") {
        continue;
      }
      throw new XIncludeSecurityError(
        "xi:include 只允许包含 xi:fallback 子元素",
      );
    }
  }

  private findAttribute(
    element: XmlElement,
    namespaceUri: string | null,
    localName: string,
  ): XmlAttribute | undefined {
    return element.attributes.find(
      (attribute) =>
        attribute.namespaceUri === namespaceUri &&
        attribute.localName === localName,
    );
  }
}
