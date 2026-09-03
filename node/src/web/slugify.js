// 与引擎一致的 slugify（主规格 §11.1）：非 [\w 一-鿿 ぀-ヿ -] → "_"，strip("_") or "book"。
export function slugifyNode(name) {
  const s = String(name ?? "").replace(/[^\w一-鿿぀-ヿ-]+/g, "_").replace(/^_+|_+$/g, "");
  return s || "book";
}
