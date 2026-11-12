import { XML_NAMESPACE, type XmlElement, type XmlNode } from "./xml-parser.ts";

/**
 * 将内部 XML 树序列化回字符串。
 *
 * - 保留元素自身的命名空间声明，使换前缀/默认命名空间的文档仍可独立解析；
 * - 剥离 xml:base 属性：XInclude 处理不承认 xml:base 对解析基准的影响，
 *   输出时也不保留该属性，避免下游组件被误导；
 * - 文本与属性统一转义，parse="text" 内联的内容无法注入新的标记。
 */

export function serializeXml(root: XmlElement): string {
  return serializeNode(root);
}

function serializeNode(node: XmlNode): string {
  if (node.kind === "text") {
    return escapeText(node.text);
  }
  return serializeElement(node);
}

function serializeElement(element: XmlElement): string {
  let output = `<${element.qname}`;

  for (const [prefix, uri] of Object.entries(
    element.namespaceDeclarations,
  )) {
    if (prefix === "") {
      output += ` xmlns="${escapeAttribute(uri)}"`;
    } else {
      output += ` xmlns:${prefix}="${escapeAttribute(uri)}"`;
    }
  }

  for (const attribute of element.attributes) {
    if (
      attribute.namespaceUri === XML_NAMESPACE &&
      attribute.localName === "base"
    ) {
      continue;
    }
    output += ` ${attribute.qname}="${escapeAttribute(attribute.value)}"`;
  }

  if (element.children.length === 0) {
    return `${output}/>`;
  }

  output += ">";
  for (const child of element.children) {
    output += serializeNode(child);
  }
  output += `</${element.qname}>`;
  return output;
}

function escapeText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function escapeAttribute(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll('"', "&quot;")
    .replaceAll("\t", "&#x9;")
    .replaceAll("\n", "&#xA;")
    .replaceAll("\r", "&#xD;");
}
