// domhandler 节点之上的 DOM 工具层（bs4 等价语义，reader 与 writer 共用）。
// 约定：Element = {type:'tag', name, attribs, children, parent, prev, next}；
//       Text = {type:'text', data, parent,...}；Comment = {type:'comment', data}。
import { Element, Text, Comment } from "domhandler";
import * as htmlparser2 from "htmlparser2";

export { Element, Text, Comment };

export const BLOCK_TAGS = new Set(["p", "h1", "h2", "h3", "h4", "h5", "h6", "li", "blockquote", "td", "th", "dt", "dd", "figcaption"]);
export const BLOCK_CANDIDATE_TAGS = new Set([...BLOCK_TAGS, "div"]);
export const HEADING_TAGS = new Set(["h1", "h2", "h3", "h4", "h5", "h6"]);
export const ATOMIC_INLINE_TAGS = new Set(["audio", "canvas", "embed", "hr", "iframe", "img", "math", "object", "source", "svg", "video"]);
export const VOID_ELEMENTS = new Set(["area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr"]);

export function isTag(node) {
  return node != null && (node.type === "tag" || node.type === "script" || node.type === "style");
}
export function isText(node) {
  return node != null && node.type === "text";
}
export function isComment(node) {
  return node != null && node.type === "comment";
}

// ---- 解析 ----

/** HTML 模式解析（等价 BeautifulSoup(html, "html.parser")，容错）。返回 domhandler Document。 */
export function parseHTML(html) {
  return htmlparser2.parseDocument(html, { decodeEntities: true });
}

/** XML 模式解析（等价 xml.etree，保留大小写与前缀名）。 */
export function parseXML(text) {
  return htmlparser2.parseDocument(text, { xmlMode: true });
}

/** localname：剥离 {ns} 与 prefix:（等价 Python local(tag)；prefix 剥离用于 dc:title）。 */
export function localName(tag) {
  let t = tag;
  const brace = t.lastIndexOf("}");
  if (brace >= 0) t = t.slice(brace + 1);
  const colon = t.indexOf(":");
  if (colon >= 0) t = t.slice(colon + 1);
  return t;
}

// ---- 遍历 ----

/** 先序遍历（含自身）。等价 bs4 find_all(True) / ET iter()。 */
export function walk(node, fn) {
  if (node == null) return;
  fn(node);
  if (node.children) for (const c of node.children) walk(c, fn);
}

/** 先序收集满足谓词的元素。includeSelf=false 时仅后代（等价 bs4 find_all）。 */
export function findAllElements(node, pred, { includeSelf = false } = {}) {
  const out = [];
  const visit = (n) => {
    if (isTag(n) && pred(n)) out.push(n);
    for (const c of n.children ?? []) visit(c);
  };
  if (includeSelf) visit(node);
  else for (const c of node?.children ?? []) visit(c);
  return out;
}

export function findElementsByName(node, names) {
  const set = names instanceof Set ? names : new Set(names);
  return findAllElements(node, (n) => set.has(n.name));
}

/** 直接子元素（Tag）。 */
export function directChildren(node) {
  return (node?.children ?? []).filter(isTag);
}

/** 第一个名字匹配的直接子元素。 */
export function findDirect(node, name) {
  for (const c of directChildren(node)) if (c.name === name || localName(c.name) === name) return c;
  return null;
}

/** 沿父链上爬（不含自身）。 */
export function ancestors(node) {
  const out = [];
  let p = node?.parent;
  while (p != null && p.type !== "root") {
    out.push(p);
    p = p.parent;
  }
  return out;
}

/** 元素自身或任一祖先在集合内。 */
export function selfOrAncestorIn(node, names) {
  if (names.has(node?.name)) return true;
  return ancestors(node).some((a) => names.has(a.name));
}

// ---- 文本 ----

export function rlen(s) {
  // Unicode 码点长度（等价 Python len(str)）
  let n = 0;
  for (const _ of s) n++;
  return n;
}

export function cpSlice(s, start, end) {
  // 码点切片（等价 Python s[start:end]）
  const cps = Array.from(s);
  if (end == null) end = cps.length;
  return cps.slice(start, end).join("");
}

/** 元素全文（等价 get_text("", strip=True)：拼接全部后代文本并 strip）。 */
export function getText(node, { strip = true } = {}) {
  let out = "";
  walk(node, (n) => {
    if (isText(n)) out += n.data;
  });
  return strip ? out.trim() : out;
}

/** 码点数文本长度（get_text(strip=True) 的长度用）。 */
export function textLength(node) {
  return rlen(getText(node));
}

// ---- 节点操作 ----

function detach(node) {
  const p = node.parent;
  if (p == null) return;
  const i = p.children.indexOf(node);
  if (i >= 0) p.children.splice(i, 1);
  if (node.prev) node.prev.next = node.next;
  if (node.next) node.next.prev = node.prev;
  node.prev = node.next = null;
}

/** 移除节点并返回（等价 bs4 extract()）。 */
export function extract(node) {
  detach(node);
  node.parent = null;
  return node;
}

/** 在 ref 前插入节点（等价 insert_before）。 */
export function insertBefore(ref, node) {
  const p = ref.parent;
  if (p == null) throw new Error("insertBefore: ref 无父节点");
  const i = p.children.indexOf(ref);
  p.children.splice(i, 0, node);
  node.parent = p;
  node.prev = ref.prev;
  node.next = ref;
  if (ref.prev) ref.prev.next = node;
  ref.prev = node;
}

/** 在 ref 后插入节点（等价 insert_after）。 */
export function insertAfter(ref, node) {
  const p = ref.parent;
  if (p == null) throw new Error("insertAfter: ref 无父节点");
  const i = p.children.indexOf(ref);
  p.children.splice(i + 1, 0, node);
  node.parent = p;
  node.prev = ref;
  node.next = ref.next;
  if (ref.next) ref.next.prev = node;
  ref.next = node;
}

/** 追加子节点到末尾（等价 bs4 append；维护 prev/next/parent）。 */
export function appendChild(parent, node) {
  node.parent = parent;
  node.next = null;
  node.prev = parent.children.length ? parent.children[parent.children.length - 1] : null;
  if (node.prev) node.prev.next = node;
  parent.children.push(node);
}

/** 在指定下标插入子节点（等价 bs4 el.insert(i, node)）。 */
export function insertAt(parent, index, node) {
  const i = Math.min(Math.max(index, 0), parent.children.length);
  node.parent = parent;
  if (i === parent.children.length) {
    appendChild(parent, node);
    return;
  }
  const ref = parent.children[i];
  parent.children.splice(i, 0, node);
  node.prev = ref.prev;
  node.next = ref;
  if (ref.prev) ref.prev.next = node;
  ref.prev = node;
}

/** 清空元素全部子节点（等价 bs4 clear）。 */
export function clearChildren(el) {
  for (const c of el.children ?? []) {
    c.parent = null;
    c.prev = c.next = null;
  }
  el.children = [];
}

/** 用 nodes 替换元素（等价 bs4 unwrap = 用子节点替换自身）。 */
export function unwrap(el) {
  const p = el.parent;
  if (p == null) return;
  const i = p.children.indexOf(el);
  const kids = el.children;
  p.children.splice(i, 1, ...kids);
  let prev = el.prev;
  for (const k of kids) {
    k.parent = p;
    k.prev = prev;
    if (prev) prev.next = k;
    prev = k;
  }
  prev.next = el.next;
  if (el.next) el.next.prev = prev;
  el.parent = el.prev = el.next = null;
}

export function createElement(name, attribs = {}) {
  return new Element(name, attribs);
}

export function createText(data) {
  return new Text(data);
}

// ---- 序列化（bs4 html.parser 风格：空元素输出 <name .../>）----

function escapeText(s) {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

function escapeAttr(s) {
  return String(s).replace(/&/g, "&amp;").replace(/"/g, "&quot;");
}

export function serializeNode(node) {
  if (isText(node)) return escapeText(node.data);
  if (isComment(node)) return `<!--${node.data}-->`;
  if (node != null && node.type === "directive") {
    // dom-serializer 语义：data 自带前缀符（"!DOCTYPE html" / "?xml …?"），直接 '<'+data+'>'
    return `<${node.data}>`;
  }
  if (!isTag(node)) return "";
  const attrs = Object.entries(node.attribs ?? {})
    .map(([k, v]) => (v === "" ? ` ${k}` : ` ${k}="${escapeAttr(v)}"`))
    .join("");
  // bs4 风格：仅 void 元素自闭合；空的非 void 元素输出 <span></span>，
  // 否则 <span/> 会被 html 模式解析器当成未闭合开始标签、吞掉后续兄弟内容。
  if (node.children.length === 0) {
    return VOID_ELEMENTS.has(node.name) ? `<${node.name}${attrs}/>` : `<${node.name}${attrs}></${node.name}>`;
  }
  const inner = node.children.map(serializeNode).join("");
  return `<${node.name}${attrs}>${inner}</${node.name}>`;
}

/** 序列化整个文档（模板 HTML）：拼接全部顶层子节点（跳过纯空白文本）。 */
export function serializeDocument(doc) {
  return (doc.children ?? [])
    .filter((c) => !(isText(c) && !c.data.trim()))
    .map(serializeNode)
    .join("");
}

// 空白字符集（Python " \t\r\n\f\v"）
export const WS_CHARS = new Set([" ", "\t", "\r", "\n", "\f", "\v"]);
