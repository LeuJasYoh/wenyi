// EPUB 目录解析与链接定位（主规格 §7.6.2 / 分册 03 §1.6）。
import { parseXML, parseHTML, isTag, findAllElements, findDirect, directChildren, localName, getText } from "./dom.js";

// ---- posix path 辅助 ----

export function posixDirname(p) {
  const i = p.lastIndexOf("/");
  if (i < 0) return ".";
  if (i === 0) return "/";
  return p.slice(0, i);
}

export function posixBasename(p) {
  const i = p.lastIndexOf("/");
  return i < 0 ? p : p.slice(i + 1);
}

export function posixNormpath(p) {
  const abs = p.startsWith("/");
  const out = [];
  for (const part of p.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") {
      if (out.length && out[out.length - 1] !== "..") out.pop();
      else if (!abs) out.push("..");
      continue;
    }
    out.push(part);
  }
  let s = out.join("/");
  if (abs) s = "/" + s;
  return s === "" ? "." : s;
}

/** Python unquote 等价（+ 不变；无效 % 序列原样保留）。 */
export function unquote(s) {
  try {
    return decodeURIComponent(s);
  } catch {
    return s.replace(/%(?:[0-9a-fA-F]{2})/g, (m) => String.fromCharCode(parseInt(m.slice(1), 16)));
  }
}

/** Python urlsplit 的最小等价（仅需 scheme/netloc/path/fragment）。 */
function urlsplitInfo(raw) {
  let rest = String(raw ?? "");
  let fragment = "";
  const hi = rest.indexOf("#");
  if (hi >= 0) {
    fragment = rest.slice(hi + 1);
    rest = rest.slice(0, hi);
  }
  let scheme = null;
  const sm = rest.match(/^([a-zA-Z][a-zA-Z0-9+.\-]*):/);
  if (sm) {
    scheme = sm[1].toLowerCase();
    rest = rest.slice(sm[0].length);
  }
  let netloc = "";
  if (rest.startsWith("//")) {
    const end = rest.search(/[/?#]/);
    netloc = end < 0 ? rest.slice(2) : rest.slice(2, end);
  }
  return { scheme, netloc, path: rest, fragment };
}

// ---- resolve_epub_href ----

export class ResolvedEpubHref {
  constructor(rawHref, resourceHref, fragment, external = false) {
    this.raw_href = rawHref;
    this.resource_href = resourceHref;
    this.fragment = fragment;
    this.external = external;
  }

  get target_key() {
    if (!this.resource_href) return "";
    return this.fragment ? `${this.resource_href}#${this.fragment}` : this.resource_href;
  }
}

/** 解析目录相对 href 为 zip 内路径（主规格 §7.6.2）。 */
export function resolveEpubHref(basePath, rawHref) {
  const raw = rawHref || "";
  const parsed = urlsplitInfo(raw);
  const external = Boolean(parsed.scheme || parsed.netloc);
  const fragment = unquote(parsed.fragment);
  if (external) return new ResolvedEpubHref(raw, "", fragment, true);
  const decodedPath = unquote(parsed.path);
  let resource;
  if (decodedPath.startsWith("/")) {
    resource = posixNormpath(decodedPath.slice(1));
  } else if (decodedPath !== "") {
    resource = posixNormpath(`${posixDirname(basePath)}/${decodedPath}`);
  } else {
    resource = posixNormpath(basePath);
  }
  if (resource === ".") resource = "";
  return new ResolvedEpubHref(raw, resource, fragment, false);
}

// ---- 目录节点构造 ----

/** 构造可 JSON 序列化目录节点（键序固定）。rawHref 为空时用空值。 */
function makeEntry({ tocPath, nodeIndex, nodeId, parentIndex, depth, kind, title, rawHref }) {
  const entry = {
    entry_id: `${tocPath}:${nodeIndex}`,
    toc_path: tocPath,
    node_index: nodeIndex,
    node_id: nodeId,
    parent_index: parentIndex,
    depth,
    kind,
    title,
    raw_href: rawHref,
    resource_href: "",
    fragment: "",
    target_key: "",
    external: false,
  };
  if (rawHref) {
    const resolved = resolveEpubHref(tocPath, rawHref);
    entry.resource_href = resolved.resource_href;
    entry.fragment = resolved.fragment;
    entry.target_key = resolved.target_key;
    entry.external = resolved.external;
  }
  return entry;
}

// ---- NCX 解析 ----

function parseNcx(doc, tocPath) {
  const entries = [];
  let navMap = null;
  for (const el of findAllElements(doc, () => true)) {
    if (localName(el.name) === "navMap") {
      navMap = el;
      break;
    }
  }
  if (!navMap) return entries;

  function visit(node, depth, parentIndex) {
    const nodeIndex = entries.length;
    const label = findDirect(node, "navLabel");
    let title = "";
    if (label) {
      let t = "";
      for (const el of findAllElements(label, () => true)) {
        if (localName(el.name) === "text") t += getText(el, { strip: false });
      }
      title = t.trim();
    }
    const content = findDirect(node, "content");
    const rawHref = content?.attribs?.["src"] ?? "";
    const nodeId = node.attribs?.["id"] ?? "";
    entries.push(makeEntry({ tocPath, nodeIndex, nodeId, parentIndex, depth, kind: "ncx", title, rawHref }));
    for (const child of directChildren(node)) {
      if (localName(child.name) === "navPoint") visit(child, depth + 1, nodeIndex);
    }
  }

  for (const child of directChildren(navMap)) {
    if (localName(child.name) === "navPoint") visit(child, 0, null);
  }
  return entries;
}

// ---- NAV 解析 ----

/** NAV 搜索范围（reader 与 writer 共用）。 */
export function navTocScopes(doc) {
  const typed = [];
  let firstNav = null;
  for (const el of findAllElements(doc, (n) => n.name === "nav")) {
    if (firstNav === null) firstNav = el;
    const types = `${el.attribs?.["epub:type"] ?? ""} ${el.attribs?.["type"] ?? ""}`.split(/\s+/);
    if (types.includes("toc")) typed.push(el);
  }
  if (typed.length) return typed;
  if (firstNav) return [firstNav];
  return [doc];
}

/** scope 的根列表：先直接子 ol，再宽容找后代 ol。 */
export function navRootList(scope) {
  const direct = directChildren(scope).find((c) => c.name === "ol");
  if (direct) return direct;
  return findAllElements(scope, (n) => n.name === "ol")[0] ?? null;
}

function parseNav(doc, tocPath) {
  const entries = [];
  function labelGetText(label) {
    // 等价 get_text(" ", strip=True)
    const parts = [];
    for (const n of iterAll(label)) {
      if (n.type === "text") parts.push(n.data.trim());
    }
    return parts.join(" ");
  }
  function* iterAll(node) {
    yield node;
    for (const c of node.children ?? []) yield* iterAll(c);
  }
  function visitLi(li, depth, parentIndex) {
    const label = directChildren(li).find((c) => c.name === "a") ??
      directChildren(li).find((c) => c.name === "span");
    let currentParent = parentIndex;
    if (label) {
      const nodeIndex = entries.length;
      const nodeId = li.attribs?.["id"] ?? "";
      const rawHref = label.name === "a" ? label.attribs?.["href"] ?? "" : "";
      const title = labelGetText(label);
      entries.push(makeEntry({ tocPath, nodeIndex, nodeId, parentIndex, depth, kind: "nav", title, rawHref }));
      currentParent = nodeIndex;
    }
    const childOl = directChildren(li).find((c) => c.name === "ol");
    if (childOl) {
      for (const childLi of directChildren(childOl)) {
        if (childLi.name === "li") visitLi(childLi, depth + 1, currentParent);
      }
    }
  }
  for (const scope of navTocScopes(doc)) {
    const rootList = navRootList(scope);
    if (!rootList) continue;
    for (const li of directChildren(rootList)) {
      if (li.name === "li") visitLi(li, 0, null);
    }
  }
  return entries;
}

// ---- 分派 ----

/** 嗅探 .xml 扩展的 NCX：根 localname 为 ncx 或存在 navMap 后代。 */
function looksLikeNcx(doc) {
  const rootTag = (doc.children ?? []).find(isTag);
  if (rootTag && localName(rootTag.name) === "ncx") return true;
  return findAllElements(doc, (n) => localName(n.name) === "navMap").length > 0;
}

/** files: Map<zipPath, Buffer>。每份目录独立容错。 */
export function parseTocEntries(files, tocPaths) {
  const out = [];
  for (const tocPath of tocPaths) {
    const data = files.get(tocPath);
    if (data == null) continue;
    let isNcx = tocPath.toLowerCase().endsWith(".ncx");
    let sniffDoc = null;
    try {
      sniffDoc = parseXML(data.toString("utf-8"));
    } catch {
      sniffDoc = null;
    }
    if (sniffDoc != null && !isNcx) isNcx = looksLikeNcx(sniffDoc);
    try {
      if (isNcx && sniffDoc != null) {
        out.push(...parseNcx(sniffDoc, tocPath));
      } else {
        // NAV 用容错 HTML 解析（等价 BeautifulSoup html.parser）
        out.push(...parseNav(parseHTML(data.toString("utf-8")), tocPath));
      }
    } catch {
      // 损坏目录跳过，不阻断其它目录
    }
  }
  return out;
}
