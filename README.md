# 网页报价证据解析包

面向 TypeScript 抽取管线的纯函数包：输入带可见性、计算样式和属性来源的 DOM 快照，输出可追溯到原始节点与字符区间的报价证据。包不依赖 DOM 实现，也不依赖任何运行时依赖。

核心约束：

- 只在同一个商品作用域内组合金额、币种和标记字段。
- 当前价必须由配置的当前价标记命中；仅凭“看起来像价格”不会成为当前价。
- `line-through`、`<s>/<strike>/<del>`、旧价格标记、隐藏节点或隐藏祖先、模板内容、模板/占位属性来源，都会被记录为候选但不会成为当前价。
- 小数点和分组符必须由站点配置显式给出。未配置的分隔符不会被猜测，相关文本作为带 `invalid-number` 原因的候选返回。
- 一个作用域内出现多个不同金额或币种时，产品状态为 `ambiguous`，`price` 为空，候选保留全部证据。
- 每个金额和可用的币种证据都带 `nodeId`、`start`、`end`、`text`、`source`（`text` 或 `attribute`）。

## 安装与调用

```ts
import { extractPrices } from './index.ts';

const result = extractPrices(
  {
    root: {
      id: 'page',
      tagName: 'div',
      children: [
        {
          id: 'product-1',
          tagName: 'div',
          attributes: { 'data-product': 'sku-1' },
          children: [
            {
              id: 'current-1',
              tagName: 'span',
              attributes: { class: 'current-price' },
              children: [{ id: 'text-1', type: 'text', text: '$1,234.50' }],
            },
            {
              id: 'old-1',
              tagName: 'span',
              attributes: { class: 'old-price', style: 'text-decoration: line-through' },
              children: [{ id: 'old-text-1', type: 'text', text: '$1,499.00' }],
            },
          ],
        },
      ],
    },
  },
  {
    productScope: '[data-product]',
    currentPriceMarker: '.current-price',
    originalPriceMarker: '.old-price',
    currency: 'USD',
    numberFormat: { decimal: '.', grouping: ',' },
  },
);

result.prices[0].value;       // 1234.5
result.prices[0].currency;    // "USD"
result.prices[0].nodeId;      // "text-1"
result.prices[0].start;       // 1
result.prices[0].end;         // 9
result.prices[0].span.text;   // "1,234.50"
```

## 快照模型

快照是普通 JSON 对象，最常用字段如下：

```ts
interface SnapshotNode {
  id?: string;                 // 追溯时原样返回
  tagName?: string;            // 或 tag
  type?: 'element' | 'text' | 'document' | 'template';
  attributes?: Record<string, string | {
    value: string;
    source?: string;           // "dom" / "template" / "computed" ...
    provenance?: string;
    fromTemplate?: boolean;
  }>;
  attributeSources?: Record<string, string>; // 属性来源的另一种写法
  text?: string;               // 文本节点，或无子节点的叶子元素文本
  children?: SnapshotNode[];
  content?: SnapshotNode | SnapshotNode[];   // <template> 内容
  style?: string | Record<string, string | number | boolean | null>;
  styles?: ...;
  computedStyle?: ...;
  visible?: boolean;
  hidden?: boolean;
  visibility?: boolean | string | {
    visible?: boolean;
    hidden?: boolean;
    display?: string;
    visibility?: string;
    opacity?: number | string;
  };
  inTemplate?: boolean;
  fromTemplate?: boolean;
  provenance?: string | { kind?: string; source?: string; fromTemplate?: boolean };
}
```

也接受 `root`、`document`、`html` 包装，以及带 `parentId` 的扁平节点列表。

## 规则

```ts
interface PricingRules {
  productScope: string | string[] | Matcher;       // 商品作用域
  currentPriceMarker: string | string[] | Matcher; // 当前价标记
  originalPriceMarker?: ...;                       // 可选：划线原价标记
  currency: 'USD' | { code: 'USD', symbol?: '$' } | string[];
  numberFormat: {
    decimal: '.',                                  // 必填，且必须单字符
    grouping: ',' | '.' | ' ' | null,              // 显式声明；null 表示无分组符
    groupingSize?: number;                         // 默认 3
    groupingSizes?: number[];                      // 从右向左，如印度式 [3, 2]
  };
  priceAttributes?: string[];                      // 额外允许读取的金额属性
  currentPriceAttribute?: string | string[];       // 属性存在即视为当前价标记
  requireCurrentMarker?: boolean;                  // 默认 true
  requireCurrency?: boolean;                       // 默认 true
}
```

选择器支持标签、`#id`、`.class`、`[attr]`、`[attr="v"]`、`~= ^= $= *= |=`、`:not(...)`、后代/子代/相邻兄弟组合，以及逗号分组。也可以直接传 `(node) => boolean` 谓词。

币种码建议使用 ISO 4217（`USD`、`EUR`、`CNY` 等）。只配置 `$`、`¥` 这类多义符号时，结果会保留 `currency-ambiguous` 候选，而不会默认成某个币种。

## 结果

```ts
interface PriceExtractionResult {
  prices: ResolvedPrice[];       // 每个成功解析的产品作用域一条
  products: ProductExtraction[]; // resolved / ambiguous / missing / invalid
  candidates: PriceCandidate[];  // 全部证据（默认包含被拒绝证据）
  ambiguous: PriceCandidate[];   // 导致歧义的候选
  issues: ExtractionIssue[];     // 配置或选择器问题
}
```

`PriceCandidate` 同时包含：

- `value`、`currencyCode`、`eligible`、`reasons`；
- 原始面：`nodeId`、`start`、`end`、`text`、`source`、`attribute`；
- 上下文：`markerIds`、`originalMarkerIds`、`fieldNodeId`；
- 金额问题：`issues`（如 `invalid-group-size`、`unconfigured-separator`）。

偏移量是 UTF-16 code unit，`end` 为排他边界；文本证据的区间落在 `node.text`，属性证据的区间落在对应属性值上。

## 常见拒绝原因

- `hidden` / `hidden-ancestor` / `display-none` / `visibility-hidden`
- `template`
- `strikethrough` / `original-price`
- `placeholder`
- `attribute-provenance`
- `invalid-number`，具体原因见 `candidate.issues`
- `currency-mismatch` / `currency-ambiguous` / `missing-currency`
- `unmarked`

## 测试

```bash
npm test
```

测试覆盖嵌套商品卡、重复金额、隐藏祖先、模板/占位、删除线、混合币种、同作用域币种字段组合、配置化小数点/分组符，以及文本与属性证据的精确字符区间。
