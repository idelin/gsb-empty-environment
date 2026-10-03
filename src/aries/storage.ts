export interface Page {
  data: number;
  pageLSN: number;
}

export interface BufferedPage extends Page {
  dirty: boolean;
}

export type DiskPages = Map<string, Page>;

export function copyPage(page: Page): Page {
  return { data: page.data, pageLSN: page.pageLSN };
}
