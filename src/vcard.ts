export interface VCardProperty {
  group: string | null;
  name: string;
  params: string;
  value: string;
  raw: string;
}

export interface VCard {
  properties: VCardProperty[];
}

const STRUCTURAL_LINE = /^(BEGIN|END):VCARD$/i;

export function parseVCard(input: string): VCard {
  const normalized = input.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const foldedLines = normalized.split("\n");

  const unfoldedLines: string[] = [];
  for (const line of foldedLines) {
    if ((line.startsWith(" ") || line.startsWith("\t")) && unfoldedLines.length > 0) {
      unfoldedLines[unfoldedLines.length - 1] += line.slice(1);
    } else {
      unfoldedLines.push(line);
    }
  }

  const properties: VCardProperty[] = [];
  for (const line of unfoldedLines) {
    if (line === "" || STRUCTURAL_LINE.test(line)) {
      continue;
    }
    const colonIndex = line.indexOf(":");
    if (colonIndex < 0) {
      continue;
    }
    const namePart = line.slice(0, colonIndex);
    const value = line.slice(colonIndex + 1);

    let group: string | null = null;
    let nameAndParams = namePart;
    const dotIndex = namePart.indexOf(".");
    if (dotIndex >= 0) {
      group = namePart.slice(0, dotIndex);
      nameAndParams = namePart.slice(dotIndex + 1);
    }

    const semicolonIndex = nameAndParams.indexOf(";");
    const name = (
      semicolonIndex >= 0 ? nameAndParams.slice(0, semicolonIndex) : nameAndParams
    ).toUpperCase();
    const params = semicolonIndex >= 0 ? nameAndParams.slice(semicolonIndex + 1) : "";

    properties.push({ group, name, params, value, raw: line });
  }

  return { properties };
}

export function unescapeText(value: string): string {
  return value
    .replace(/\\\\/g, "\u0000")
    .replace(/\\n/gi, "\n")
    .replace(/\\,/g, ",")
    .replace(/\\;/g, ";")
    .replace(/\u0000/g, "\\");
}

export function serializeVCard(properties: VCardProperty[]): string {
  const hasVersion = properties.some((property) => property.name === "VERSION");
  const lines = ["BEGIN:VCARD"];
  if (!hasVersion) {
    lines.push("VERSION:3.0");
  }
  for (const property of properties) {
    lines.push(property.raw);
  }
  lines.push("END:VCARD");
  return lines.join("\r\n") + "\r\n";
}
