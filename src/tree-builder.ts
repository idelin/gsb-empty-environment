import type {
  Attribute,
  ChildNode,
  DocumentNode,
  ElementNode,
  Namespace,
  SourceLocation,
} from './types.ts';
import { NS_HTML, NS_MATHML, NS_SVG } from './types.ts';
import type { RawAttribute, TokenHandler } from './tokenizer.ts';
import { Tokenizer } from './tokenizer.ts';

/** Tag name adjustments required for SVG elements per HTML5 spec. */
const SVG_TAG_ADJUSTMENTS: Readonly<Record<string, string>> = {
  altglyph: 'altGlyph',
  altglyphdef: 'altGlyphDef',
  altglyphitem: 'altGlyphItem',
  animatecolor: 'animateColor',
  animatemotion: 'animateMotion',
  animatetransform: 'animateTransform',
  clippath: 'clipPath',
  feblend: 'feBlend',
  fecolormatrix: 'feColorMatrix',
  fecomponenttransfer: 'feComponentTransfer',
  fecomposite: 'feComposite',
  feconvolvematrix: 'feConvolveMatrix',
  fediffuselighting: 'feDiffuseLighting',
  fedisplacementmap: 'feDisplacementMap',
  fedistantlight: 'feDistantLight',
  feflood: 'feFlood',
  fefunca: 'feFuncA',
  fefuncb: 'feFuncB',
  fefuncg: 'feFuncG',
  fefuncr: 'feFuncR',
  fegaussianblur: 'feGaussianBlur',
  feimage: 'feImage',
  femerge: 'feMerge',
  femergenode: 'feMergeNode',
  femorphology: 'feMorphology',
  feoffset: 'feOffset',
  fepointlight: 'fePointLight',
  fespecularlighting: 'feSpecularLighting',
  fespotlight: 'feSpotLight',
  fetile: 'feTile',
  feturbulence: 'feTurbulence',
  foreignobject: 'foreignObject',
  glyphref: 'glyphRef',
  lineargradient: 'linearGradient',
  radialgradient: 'radialGradient',
  textpath: 'textPath',
};

const SVG_ATTRIBUTE_ADJUSTMENTS: Readonly<Record<string, string>> = {
  attributename: 'attributeName',
  attributetype: 'attributeType',
  basefrequency: 'baseFrequency',
  baseprofile: 'baseProfile',
  calcmode: 'calcMode',
  clippathunits: 'clipPathUnits',
  diffuseconstant: 'diffuseConstant',
  edgemode: 'edgeMode',
  filterunits: 'filterUnits',
  glyphref: 'glyphRef',
  gradienttransform: 'gradientTransform',
  gradientunits: 'gradientUnits',
  kernelmatrix: 'kernelMatrix',
  kernelunitlength: 'kernelUnitLength',
  keypoints: 'keyPoints',
  keysplines: 'keySplines',
  keytimes: 'keyTimes',
  lengthadjust: 'lengthAdjust',
  limitingconeangle: 'limitingConeAngle',
  markerheight: 'markerHeight',
  markerunits: 'markerUnits',
  markerwidth: 'markerWidth',
  maskcontentunits: 'maskContentUnits',
  maskunits: 'maskUnits',
  numoctaves: 'numOctaves',
  pathlength: 'pathLength',
  patterncontentunits: 'patternContentUnits',
  patterntransform: 'patternTransform',
  patternunits: 'patternUnits',
  pointsatx: 'pointsAtX',
  pointsaty: 'pointsAtY',
  pointsatz: 'pointsAtZ',
  preservealpha: 'preserveAlpha',
  preserveaspectratio: 'preserveAspectRatio',
  primitiveunits: 'primitiveUnits',
  refx: 'refX',
  refy: 'refY',
  repeatcount: 'repeatCount',
  repeatdur: 'repeatDur',
  requiredextensions: 'requiredExtensions',
  requiredfeatures: 'requiredFeatures',
  specularconstant: 'specularConstant',
  specularexponent: 'specularExponent',
  spreadmethod: 'spreadMethod',
  startoffset: 'startOffset',
  stddeviation: 'stdDeviation',
  surfacescale: 'surfaceScale',
  systemlanguage: 'systemLanguage',
  tablevalues: 'tableValues',
  targetx: 'targetX',
  targety: 'targetY',
  textlength: 'textLength',
  viewbox: 'viewBox',
  viewtarget: 'viewTarget',
  xchannelselector: 'xChannelSelector',
  ychannelselector: 'yChannelSelector',
  zoomandpan: 'zoomAndPan',
};

const MATHML_ATTRIBUTE_ADJUSTMENTS: Readonly<Record<string, string>> = {
  definitionurl: 'definitionURL',
};

/** HTML tags that break out of MathML text integration points. */
const MATHML_TEXT_INTEGRATION_POINTS = new Set([
  'mtext', 'mi', 'mo', 'mn', 'ms',
]);

const HTML_INTEGRATION_TAGS = new Set([
  'foreignobject', 'desc', 'title',
]);

/** Tags that always pop back to HTML namespace per the "break out" list. */
const MATHML_BREAKOUT_TAGS = new Set([
  'b', 'big', 'blockquote', 'br', 'code', 'dd', 'div', 'dl', 'dt', 'em',
  'embed', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'head', 'hr', 'i', 'img',
  'li', 'listing', 'menu', 'meta', 'nobr', 'ol', 'p', 'pre', 'ruby', 's',
  'small', 'span', 'strong', 'strike', 'sub', 'sup', 'table', 'tt', 'u',
  'ul', 'var',
  'font', // handled specially with attribute checks
]);

const SVG_BREAKOUT_TAGS = new Set(['font']);

const VOID_ELEMENTS = new Set([
  'area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link',
  'meta', 'param', 'source', 'track', 'wbr',
]);

const RAWTEXT_ELEMENTS = new Set(['style', 'xmp', 'iframe', 'noembed', 'noframes']);
const SCRIPT_LIKE = new Set(['script', 'noscript']);
const RCDATA_ELEMENTS = new Set(['textarea', 'title']);
const P_CLOSERS = new Set([
  'address', 'article', 'aside', 'blockquote', 'details', 'div', 'dl',
  'fieldset', 'figcaption', 'figure', 'footer', 'form', 'h1', 'h2', 'h3',
  'h4', 'h5', 'h6', 'header', 'hgroup', 'hr', 'main', 'menu', 'nav', 'ol',
  'p', 'pre', 'section', 'table', 'ul',
]);
const P_IMPLICIT_CLOSE = new Set([
  'a', 'audio', 'del', 'ins', 'map', 'noscript', 'video',
]);

const HEAD_TAGS = new Set(['base', 'basefont', 'bgsound', 'link', 'meta', 'title']);

export interface TreeBuilderOptions {
  /** Record foster-parenting events for mXSS auditing. */
  onFosterParent?: (node: ChildNode, location: SourceLocation) => void;
  /** Record namespace break-outs (foreign content -> HTML) events. */
  onNamespaceBreakout?: (
    element: ElementNode,
    reason: 'mathml-breakout' | 'svg-breakout' | 'integration-point',
  ) => void;
}

export interface StructuralEvent {
  type: 'namespace-breakout' | 'foster-parent';
  reason: string;
  tagName?: string;
  location: SourceLocation;
}

/**
 * HTML5 fragment tree builder with full foreign-content (SVG / MathML)
 * namespace handling, including text integration points and HTML integration
 * points. Implicit HTML scaffolding (html/head/body) is intentionally not
 * materialized: this builder targets sanitizable document fragments while
 * preserving every namespace transition a browser would perform.
 */
export class TreeBuilder implements TokenHandler {
  readonly document: DocumentNode = {
    type: 'document',
    children: [],
    location: { offset: 0, line: 1, column: 1 },
  };
  private readonly openElements: ElementNode[] = [];
  /** Insertion mode captured whenever an element is pushed. */
  private readonly elementModes: string[] = [];
  private mode = 'IN_BODY';
  private readonly tokenizer: Tokenizer;
  private pendingTableText = '';
  private readonly onFosterParent?: (
    node: ChildNode,
    location: SourceLocation,
  ) => void;
  private readonly onNamespaceBreakout?: (
    element: ElementNode,
    reason: 'mathml-breakout' | 'svg-breakout' | 'integration-point',
  ) => void;
  /** Structural events relevant to mXSS auditing. */
  readonly structuralEvents: StructuralEvent[] = [];

  constructor(input: string, options: TreeBuilderOptions = {}) {
    this.tokenizer = new Tokenizer(input, this);
    this.onFosterParent = options.onFosterParent;
    this.onNamespaceBreakout = options.onNamespaceBreakout;
  }

  parse(): DocumentNode {
    this.tokenizer.run();
    return this.document;
  }

  private recordBreakout(
    element: ElementNode,
    reason: 'mathml-breakout' | 'svg-breakout' | 'integration-point',
  ): void {
    this.structuralEvents.push({
      type: 'namespace-breakout',
      reason,
      tagName: element.tagName,
      location: element.location,
    });
    this.onNamespaceBreakout?.(element, reason);
  }

  private currentNode(): ElementNode | DocumentNode {
    return this.openElements[this.openElements.length - 1] ?? this.document;
  }

  private pushMode(mode: string): void {
    this.mode = mode;
  }

  private popMode(): string {
    // Restore the mode recorded on the element now at the top of the stack.
    const index = this.openElements.length - 1;
    this.mode = this.elementModes[index] ?? 'IN_BODY';
    return this.mode;
  }

  private currentElement(): ElementNode | undefined {
    return this.openElements[this.openElements.length - 1];
  }

  private insertNode(node: ChildNode): void {
    const inTable = this.findOpenIndex('table', NS_HTML) >= 0 &&
      this.currentTableIsTopmostStructure();
    if (inTable && node.type === 'text' &&
      this.mode !== 'IN_CELL' && this.mode !== 'IN_ROW' &&
      this.mode !== 'IN_CAPTION') {
      this.handleTableText(node);
      return;
    }
    const target = this.currentNode();
    if (inTable) {
      if (node.type === 'element' &&
        !['caption', 'colgroup', 'col', 'tbody', 'tfoot', 'thead',
          'tr', 'td', 'th', 'table'].includes(node.tagName)) {
        this.fosterParent(node);
        return;
      }
    }
    target.children.push(node);
  }

  private handleTableText(node: ChildNode): void {
    if (node.type !== 'text') {
      this.insertNode(node);
      return;
    }
    this.pendingTableText += node.data;
    if (this.pendingTableText.trim() === '') {
      return;
    }
    const textNode = {
      type: 'text' as const,
      data: this.pendingTableText,
      location: node.location,
    };
    this.pendingTableText = '';
    this.fosterParent(textNode);
  }

  private fosterParent(node: ChildNode): void {
    this.onFosterParent?.(node, node.location);
    this.structuralEvents.push({
      type: 'foster-parent',
      reason: 'table-insertion-mode',
      tagName: node.type === 'element' ? node.tagName : undefined,
      location: node.location,
    });
    const tableIndex = this.findOpenIndex('table', NS_HTML);
    if (tableIndex < 0) {
      this.document.children.push(node);
      return;
    }
    const table = this.openElements[tableIndex];
    this.ensureTableAttached(table, tableIndex);
    const parent = this.findParentOf(table) ?? this.document;
    const tablePosition = parent.children.indexOf(table);
    const insertAt = tablePosition >= 0 ? tablePosition :
      parent.children.length;
    parent.children.splice(
      insertAt,
      0,
      node,
    );
  }

  private ensureTableAttached(
    table: ElementNode,
    tableIndex: number,
  ): void {
    const actualParent = this.findParentOf(table);
    if (actualParent === this.document) {
      return;
    }
    if (actualParent && actualParent !== this.document) {
      const position = actualParent.children.indexOf(table);
      if (position >= 0) {
        actualParent.children.splice(position, 1);
      }
    }
    this.document.children.push(table);
  }

  private findParentOf(
    target: ElementNode,
  ): ElementNode | DocumentNode | undefined {
    const visit = (
      node: ElementNode | DocumentNode,
    ): ElementNode | DocumentNode | undefined => {
      if (node.children.includes(target)) {
        return node;
      }
      for (const child of node.children) {
        if (child.type === 'element') {
          const found = visit(child);
          if (found) {
            return found;
          }
        }
      }
      return undefined;
    };
    return visit(this.document);
  }

  private findOpenIndex(tagName: string, namespace?: Namespace): number {
    for (let i = this.openElements.length - 1; i >= 0; i -= 1) {
      const element = this.openElements[i];
      if (element.tagName === tagName &&
        (namespace === undefined || element.namespace === namespace)) {
        return i;
      }
    }
    return -1;
  }

  onText(data: string, location: SourceLocation): void {
    const node = { type: 'text' as const, data, location };
    this.insertNode(node);
  }

  onComment(data: string, location: SourceLocation): void {
    this.currentNode().children.push({ type: 'comment', data, location });
  }

  onDoctype(location: SourceLocation): void {
    this.currentNode().children.push({
      type: 'comment',
      data: '<!doctype>',
      location,
    });
  }

  onEOF(): void {
    // Close any elements left open (fragment parsing tolerates missing end
    // tags); the nodes remain attached where they were inserted.
    while (this.openElements.length > 0) {
      this.openElements.pop();
      this.elementModes.pop();
    }
    this.mode = 'IN_BODY';
    this.pendingTableText = '';
  }

  onStartTag(
    tagName: string,
    rawAttributes: RawAttribute[],
    selfClosing: boolean,
    location: SourceLocation,
  ): void {
    const current = this.currentElement();
    // Table insertion modes take precedence even while a foreign element is
    // still on the stack (foster parenting scenario), exactly per spec.
    if (this.findOpenIndex('table', NS_HTML) >= 0 &&
      this.currentTableIsTopmostStructure()) {
      this.handleTableStartTag(
        tagName, rawAttributes, selfClosing, location,
      );
      return;
    }
    if (current && current.namespace !== NS_HTML) {
      this.handleForeignStartTag(
        tagName, rawAttributes, selfClosing, location,
      );
      return;
    }
    this.handleHtmlStartTag(tagName, rawAttributes, selfClosing, location);
  }

  private currentTableIsTopmostStructure(): boolean {
    const tableIndex = this.findOpenIndex('table', NS_HTML);
    if (tableIndex < 0) {
      return false;
    }
    // Only treat as table scope when nothing HTML-structural sits above it.
    for (let i = tableIndex + 1; i < this.openElements.length; i += 1) {
      const element = this.openElements[i];
      if (element.namespace === NS_HTML &&
        !['tbody', 'thead', 'tfoot', 'tr', 'td', 'th', 'caption',
          'colgroup', 'col'].includes(element.tagName)) {
        return false;
      }
    }
    return true;
  }

  private shouldEnterTableBody(): boolean {
    // The table is open but no tbody/thead/tfoot section exists yet.
    const tableIndex = this.findOpenIndex('table', NS_HTML);
    if (tableIndex < 0) {
      return false;
    }
    for (let i = tableIndex + 1; i < this.openElements.length; i += 1) {
      const tag = this.openElements[i].tagName;
      if (tag === 'tbody' || tag === 'thead' || tag === 'tfoot') {
        return false;
      }
    }
    return true;
  }

  onEndTag(tagName: string, location: SourceLocation): void {
    if (this.findOpenIndex('table', NS_HTML) >= 0 &&
      this.currentTableIsTopmostStructure()) {
      this.handleTableEndTag(tagName, location);
      return;
    }
    const current = this.currentElement();
    if (current && current.namespace !== NS_HTML) {
      this.handleForeignEndTag(tagName);
      return;
    }
    this.handleHtmlEndTag(tagName, location);
  }

  private handleHtmlStartTag(
    tagName: string,
    rawAttributes: RawAttribute[],
    selfClosing: boolean,
    location: SourceLocation,
  ): void {
    if (this.mode === 'IN_HEAD' && !HEAD_TAGS.has(tagName)) {
      this.popMode();
    }
    if (tagName === 'math') {
      const element = this.createElement(
        tagName, NS_MATHML, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      return;
    }
    if (tagName === 'svg') {
      const element = this.createElement(
        tagName, NS_SVG, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      return;
    }
    if (tagName === 'table') {
      this.closeParagraph();
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      const foreignParent = this.currentElement();
      if (foreignParent && foreignParent.namespace !== NS_HTML) {
        this.recordBreakout(element, 'mathml-breakout');
        // Detach from the foreign parent and attach at document level,
        // mirroring the browser's stack/tree realignment.
        foreignParent.children = foreignParent.children.filter(
          (child) => child !== element,
        );
        this.document.children.push(element);
        if (!selfClosing && !VOID_ELEMENTS.has(element.tagName)) {
          this.openElements.push(element);
          this.elementModes.push('IN_TABLE');
        } else {
          element.selfClosing = true;
        }
      } else {
        this.insertAndOpen(element, selfClosing);
      }
      if (!selfClosing) {
        this.mode = 'IN_TABLE';
      }
      this.pendingTableText = '';
      return;
    }
    if (this.mode.startsWith('IN_TABLE')) {
      this.handleTableStartTag(
        tagName, rawAttributes, selfClosing, location,
      );
      return;
    }
    if (tagName === 'p') {
      this.closeParagraph();
    } else if (P_CLOSERS.has(tagName)) {
      this.closeOpenP();
    } else if (P_IMPLICIT_CLOSE.has(tagName)) {
      this.closeSameAnchor(tagName);
    } else if (tagName === 'li') {
      this.closeOpen('li');
    } else if (tagName === 'dd' || tagName === 'dt') {
      this.closeOpen('dd');
      this.closeOpen('dt');
    } else if (tagName === 'option') {
      this.closeOpen('option');
    } else if (tagName === 'optgroup') {
      this.closeOpen('option');
      this.closeOpen('optgroup');
    } else if (tagName === 'select') {
      this.pushMode('IN_SELECT');
    }

    const element = this.createHtmlElement(
      tagName, rawAttributes, location, selfClosing,
    );
    this.insertAndOpen(element, selfClosing);
    if (!selfClosing) {
      this.configureContentModel(tagName);
    }
  }

  private handleTableStartTag(
    tagName: string,
    rawAttributes: RawAttribute[],
    selfClosing: boolean,
    location: SourceLocation,
  ): void {
    if (tagName === 'caption') {
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      if (!selfClosing) this.pushMode('IN_CAPTION');
      return;
    }
    if (tagName === 'colgroup') {
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      if (!selfClosing) this.pushMode('IN_COLUMN_GROUP');
      return;
    }
    if (tagName === 'col') {
      if (this.mode === 'IN_TABLE') {
        const colgroup = this.createHtmlElement(
          'colgroup', [], location, false,
        );
        this.insertAndOpen(colgroup, false);
        this.pushMode('IN_COLUMN_GROUP');
      }
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, true,
      );
      this.insertAndOpen(element, true);
      return;
    }
    if (tagName === 'tbody' || tagName === 'thead' || tagName === 'tfoot') {
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      if (!selfClosing) this.pushMode('IN_TABLE_BODY');
      return;
    }
    if (tagName === 'tr') {
      if (this.mode === 'IN_TABLE' || this.shouldEnterTableBody()) {
        const tbody = this.createHtmlElement('tbody', [], location, false);
        this.insertAndOpen(tbody, false);
        this.pushMode('IN_TABLE_BODY');
      }
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      if (!selfClosing) this.pushMode('IN_ROW');
      return;
    }
    if (tagName === 'td' || tagName === 'th') {
      if (this.mode === 'IN_TABLE' || this.mode === 'IN_TABLE_BODY') {
        if (this.mode === 'IN_TABLE' || this.shouldEnterTableBody()) {
          const tbody = this.createHtmlElement('tbody', [], location, false);
          this.insertAndOpen(tbody, false);
          this.pushMode('IN_TABLE_BODY');
        }
        const tr = this.createHtmlElement('tr', [], location, false);
        this.insertAndOpen(tr, false);
        this.pushMode('IN_ROW');
      }
      const element = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.insertAndOpen(element, selfClosing);
      if (!selfClosing) this.pushMode('IN_CELL');
      return;
    }

    if (tagName === 'tbody' || tagName === 'thead' || tagName === 'tfoot') {
      // Already handled above; guard keeps table scope consistent.
    }
    // Foreign-content tokens (e.g. <mglyph>) keep their namespace rules;
    // everything else is foster-parented by insertNode.
    const current = this.currentElement();
    if (current && current.namespace !== NS_HTML) {
      this.handleForeignStartTag(
        tagName, rawAttributes, selfClosing, location,
      );
      return;
    }
    this.closeParagraph();
    const element = this.createHtmlElement(
      tagName, rawAttributes, location, selfClosing,
    );
    if (this.findOpenIndex('table', NS_HTML) >= 0 &&
      this.mode.startsWith('IN_TABLE')) {
      // Unknown tag inside table scope: explicitly foster-parent.
      this.fosterParent(element);
      if (!selfClosing && !VOID_ELEMENTS.has(element.tagName)) {
        this.openElements.push(element);
        this.elementModes.push(this.mode);
      } else {
        element.selfClosing = true;
      }
    } else {
      this.insertAndOpen(element, selfClosing);
    }
    if (!selfClosing) {
      this.configureContentModel(tagName);
    }
  }

  private handleTableEndTag(
    tagName: string,
    location: SourceLocation,
  ): void {
    if (tagName === 'table') {
      const index = this.findOpenIndex('table', NS_HTML);
      if (index >= 0) {
        this.popTo(index);
      }
      return;
    }
    const tableIndex = this.findOpenIndex('table', NS_HTML);
    const sameNamespaceIndex = this.findOpenIndex(tagName);
    if (sameNamespaceIndex > tableIndex) {
      this.popTo(sameNamespaceIndex);
      return;
    }
    // End tags that do not match an element below the table are ignored.
    void location;
  }

  private handleHtmlEndTag(tagName: string, location: SourceLocation): void {
    if (tagName === 'table') {
      const index = this.findOpenIndex('table', NS_HTML);
      if (index >= 0) {
        this.popTo(index);
        while (this.mode !== 'IN_BODY') {
          this.popMode();
        }
      }
      return;
    }
    if (tagName === 'p') {
      const index = this.findOpenIndex('p', NS_HTML);
      if (index < 0) {
        // Stray </p> inserts an empty paragraph node per spec; fragments
        // instead ignore it to avoid synthetic content.
        return;
      }
      this.popTo(index);
      return;
    }
    if (tagName === 'body' || tagName === 'html') {
      return;
    }
    const index = this.findOpenIndex(tagName, NS_HTML);
    if (index >= 0) {
      this.popElementAt(index);
      if (tagName === 'select' && this.mode === 'IN_SELECT') {
        this.popMode();
      }
    } else if (location) {
      // Unmatched end tag: ignore.
    }
  }

  /** Pop exactly the element at `index`, preserving foreign ancestors. */
  private popElementAt(index: number): void {
    while (this.openElements.length > index + 1) {
      this.openElements.pop();
      this.elementModes.pop();
    }
    this.openElements.pop();
    this.elementModes.pop();
    this.popMode();
  }

  private closeParagraph(): void {
    if (this.findOpenIndex('p', NS_HTML) >= 0) {
      this.closeOpenP();
    }
  }

  private closeOpenP(): void {
    const index = this.findOpenIndex('p', NS_HTML);
    if (index >= 0) {
      this.popTo(index);
    }
  }

  private closeOpen(tagName: string): void {
    const index = this.findOpenIndex(tagName, NS_HTML);
    if (index >= 0) {
      this.popTo(index);
    }
  }

  private closeSameAnchor(tagName: string): void {
    const index = this.findOpenIndex(tagName, NS_HTML);
    if (index >= 0) {
      this.popTo(index);
    }
  }

  private popTo(index: number): void {
    while (this.openElements.length > index) {
      this.openElements.pop();
      this.elementModes.pop();
    }
    this.popMode();
  }

  /**
   * HTML5 "The rules for parsing tokens in foreign content": integration
   * point evaluation, namespace break-out and tag/attribute name adjustment.
   */
  private handleForeignStartTag(
    tagName: string,
    rawAttributes: RawAttribute[],
    selfClosing: boolean,
    location: SourceLocation,
  ): void {
    const adjustedName = this.adjustForeignTagName(tagName);
    const current = this.currentElement();
    if (!current) {
      return;
    }

    const integration = this.findIntegrationPoint();
    if (integration !== undefined) {
      if (this.isMathTextIntegrationPoint(integration)) {
        if (MATHML_BREAKOUT_TAGS.has(tagName) ||
          (tagName === 'font' && this.fontBreaksOut(rawAttributes))) {
          const breakoutElement = this.createHtmlElement(
            tagName, rawAttributes, location, selfClosing,
          );
          this.recordBreakout(breakoutElement, 'mathml-breakout');
          this.insertIntoIntegration(integration, breakoutElement,
            selfClosing);
          if (!selfClosing) {
            this.configureContentModel(tagName);
          }
          return;
        }
      } else if (this.isHtmlIntegrationPoint(integration)) {
        const htmlElement = this.createHtmlElement(
          tagName, rawAttributes, location, selfClosing,
        );
        this.recordBreakout(htmlElement, 'integration-point');
        this.insertIntoIntegration(integration, htmlElement, selfClosing);
        if (!selfClosing) {
          this.configureContentModel(tagName);
        }
        return;
      }
    }

    if (MATHML_BREAKOUT_TAGS.has(tagName) ||
      (current.namespace === NS_MATHML &&
        tagName === 'font' && this.fontBreaksOut(rawAttributes)) ||
      (current.namespace === NS_SVG && SVG_BREAKOUT_TAGS.has(tagName))) {
      this.popForeignUntilHtml();
      const breakoutElement = this.createHtmlElement(
        tagName, rawAttributes, location, selfClosing,
      );
      this.recordBreakout(
        breakoutElement,
        current.namespace === NS_SVG ? 'svg-breakout' : 'mathml-breakout',
      );
      this.insertAndOpen(breakoutElement, selfClosing);
      if (!selfClosing) {
        this.configureContentModel(tagName);
      }
      return;
    }

    const namespace = current.namespace;
    const element = this.createElement(
      adjustedName, namespace, rawAttributes, location, selfClosing,
    );
    if (this.mode.startsWith('IN_TABLE') &&
      this.findOpenIndex('table', NS_HTML) >= 0) {
      // Foreign elements encountered while a table is the structural
      // container are foster-parented, matching browser behavior.
      this.fosterParent(element);
      if (!selfClosing) {
        this.openElements.push(element);
        this.elementModes.push(this.mode);
      } else {
        element.selfClosing = true;
      }
      return;
    }
    this.insertAndOpen(element, selfClosing);
  }

  private handleForeignEndTag(tagName: string): void {
    const index = this.findForeignIndex(tagName);
    if (index >= 0) {
      this.popTo(index);
      return;
    }
    // Unrecognized end tags in foreign content are ignored per spec unless
    // they target HTML integration content already on the stack.
    const htmlIndex = this.findOpenIndex(tagName, NS_HTML);
    if (htmlIndex >= 0 && this.openElements.slice(htmlIndex).some(
      (element) => element.namespace === NS_HTML,
    )) {
      this.popTo(htmlIndex);
    }
  }

  private findForeignIndex(tagName: string): number {
    const current = this.currentElement();
    if (!current) {
      return -1;
    }
    const target = current.namespace === NS_SVG
      ? this.adjustForeignTagName(tagName)
      : tagName;
    for (let i = this.openElements.length - 1; i >= 0; i -= 1) {
      const element = this.openElements[i];
      if (element.namespace === NS_HTML) {
        return -1;
      }
      if (element.tagName === target) {
        return i;
      }
    }
    return -1;
  }

  private popForeignUntilHtml(): void {
    while (this.openElements.length > 0) {
      const element = this.openElements[this.openElements.length - 1];
      if (element.namespace === NS_HTML ||
        this.isHtmlIntegrationPoint(element)) {
        return;
      }
      this.openElements.pop();
      this.elementModes.pop();
    }
  }

  /** Insert HTML content inside an active integration point element. */
  private insertIntoIntegration(
    integration: ElementNode,
    element: ElementNode,
    selfClosing: boolean,
  ): void {
    integration.children.push(element);
    const isVoid = element.namespace === NS_HTML &&
      VOID_ELEMENTS.has(element.tagName);
    if (!selfClosing && !isVoid) {
      this.openElements.push(element);
      this.elementModes.push(this.mode);
    } else {
      element.selfClosing = true;
    }
  }

  /**
   * Walk the open-element stack bottom-up to find the nearest active
   * integration point, exactly as described in the HTML5 spec.
   */
  private findIntegrationPoint(): ElementNode | undefined {
    for (let i = this.openElements.length - 1; i >= 0; i -= 1) {
      const element = this.openElements[i];
      if (element.namespace === NS_HTML) {
        return undefined;
      }
      if (element.namespace === NS_MATHML) {
        if (MATHML_TEXT_INTEGRATION_POINTS.has(element.tagName)) {
          return element;
        }
        if (element.tagName === 'annotation-xml' &&
          element.attributes.some(
            (attr) => attr.name === 'encoding' &&
              ['text/html', 'application/xhtml+xml']
                .includes(attr.value),
          )) {
          return element;
        }
      } else if (element.namespace === NS_SVG) {
        if (element.tagName === 'foreignObject' ||
          HTML_INTEGRATION_TAGS.has(element.tagName)) {
          return element;
        }
      }
    }
    return undefined;
  }

  private isHtmlIntegrationPoint(element: ElementNode): boolean {
    if (element.namespace === NS_SVG &&
      (element.tagName === 'foreignObject' ||
        HTML_INTEGRATION_TAGS.has(element.tagName))) {
      return true;
    }
    if (element.namespace === NS_MATHML &&
      element.tagName === 'annotation-xml' &&
      element.attributes.some(
        (attr) => attr.name === 'encoding' &&
          ['text/html', 'application/xhtml+xml'].includes(attr.value),
      )) {
      return true;
    }
    return false;
  }

  private isMathTextIntegrationPoint(element: ElementNode): boolean {
    return element.namespace === NS_MATHML &&
      MATHML_TEXT_INTEGRATION_POINTS.has(element.tagName);
  }

  private fontBreaksOut(rawAttributes: RawAttribute[]): boolean {
    return rawAttributes.some(
      (attr) => attr.name === 'color' || attr.name === 'face' ||
        attr.name === 'size',
    );
  }

  private adjustForeignTagName(tagName: string): string {
    const current = this.currentElement();
    if (current?.namespace === NS_SVG) {
      return SVG_TAG_ADJUSTMENTS[tagName] ?? tagName;
    }
    return tagName;
  }

  private createHtmlElement(
    tagName: string,
    rawAttributes: RawAttribute[],
    location: SourceLocation,
    selfClosing: boolean,
  ): ElementNode {
    return this.createElement(
      tagName, NS_HTML, rawAttributes, location, selfClosing,
    );
  }

  private createElement(
    tagName: string,
    namespace: Namespace,
    rawAttributes: RawAttribute[],
    location: SourceLocation,
    selfClosing: boolean,
  ): ElementNode {
    const attributes: Attribute[] = rawAttributes.map((raw) => ({
      name: this.adjustAttributeName(raw.name, namespace),
      rawName: raw.name,
      value: raw.value,
      location: raw.location,
    }));
    return {
      type: 'element',
      tagName,
      namespace,
      attributes,
      children: [],
      selfClosing,
      location,
    };
  }

  private adjustAttributeName(name: string, namespace: Namespace): string {
    if (namespace === NS_SVG) {
      return SVG_ATTRIBUTE_ADJUSTMENTS[name] ?? name;
    }
    if (namespace === NS_MATHML) {
      return MATHML_ATTRIBUTE_ADJUSTMENTS[name] ?? name;
    }
    return name;
  }

  private insertAndOpen(
    element: ElementNode,
    selfClosing: boolean,
  ): void {
    this.insertNode(element);
    const isVoid = element.namespace === NS_HTML &&
      VOID_ELEMENTS.has(element.tagName);
    if (!selfClosing && !isVoid) {
      this.openElements.push(element);
      this.elementModes.push(this.mode);
    } else {
      element.selfClosing = true;
    }
  }

  private configureContentModel(tagName: string): void {
    if (RAWTEXT_ELEMENTS.has(tagName)) {
      this.tokenizer.setContentModel(tagName, 'rawtext');
    } else if (RCDATA_ELEMENTS.has(tagName)) {
      this.tokenizer.setContentModel(tagName, 'rcdata');
    } else if (tagName === 'script') {
      this.tokenizer.setContentModel(tagName, 'scriptdata');
    } else if (tagName === 'plaintext') {
      this.tokenizer.setContentModel(tagName, 'plaintext');
    } else if (tagName === 'noscript') {
      // In a scripting-enabled host, noscript content is raw text.
      this.tokenizer.setContentModel(tagName, 'rawtext');
    }
  }
}
