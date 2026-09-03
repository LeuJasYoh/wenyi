// 编码探测链（UnicodeDammit 等价，架构分册 §4.5(3) 固定顺序）：
// BOM → XML/HTML 声明 charset → jschardet → UTF-8 replace 兜底。
import jschardet from "jschardet";
import iconv from "iconv-lite";

const BOMS = [
  { prefix: Buffer.from([0xef, 0xbb, 0xbf]), enc: "utf-8" },
  { prefix: Buffer.from([0xff, 0xfe, 0x00, 0x00]), enc: "utf-32le" },
  { prefix: Buffer.from([0x00, 0x00, 0xfe, 0xff]), enc: "utf-32be" },
  { prefix: Buffer.from([0xff, 0xfe]), enc: "utf-16le" },
  { prefix: Buffer.from([0xfe, 0xff]), enc: "utf-16be" },
];

function bomEncoding(buf) {
  for (const { prefix, enc } of BOMS) {
    if (buf.length >= prefix.length && buf.subarray(0, prefix.length).equals(prefix)) return enc;
  }
  return null;
}

function declaredEncoding(buf) {
  const head = buf.subarray(0, 4096).toString("latin1");
  const m =
    head.match(/<\?xml[^>]*encoding\s*=\s*['"]([^'"]+)['"]/i) ||
    head.match(/<meta[^>]+charset\s*=\s*['"]?([\w-]+)/i);
  return m ? m[1] : null;
}

function decodeWith(buf, enc) {
  try {
    if (!enc || !iconv.encodingExists(enc)) return null;
    if (/^(utf-?8|ascii|us-ascii)$/i.test(String(enc))) return null; // 交给主链路处理
    return iconv.decode(buf, enc);
  } catch {
    return null;
  }
}

/** 等价 _decode_markup：UnicodeDammit(...).unicode_markup，None → utf-8 replace 兜底。 */
export function decodeMarkup(buf) {
  const bom = bomEncoding(buf);
  if (bom) {
    const text = decodeWith(buf, bom);
    if (text != null) return text;
  }
  const decl = declaredEncoding(buf);
  if (decl) {
    const text = decodeWith(buf, decl);
    if (text != null && !text.includes("\uFFFD")) return text;
  }
  try {
    const det = jschardet.detect(buf.subarray(0, 64 * 1024));
    if (det && det.encoding && det.confidence > 0.5) {
      const text = decodeWith(buf, det.encoding);
      if (text != null && !text.includes("\uFFFD")) return text;
    }
  } catch {
    // 探测失败走兜底
  }
  return buf.toString("utf-8");
}

/** FB2 专用：从 XML 声明读编码（单双引号），失败回退 UTF-8 replace。 */
export function decodeFB2(buf) {
  const m = buf.toString("latin1").match(/<\?xml.*?encoding\s*=\s*['"]([^'"]+)['"]/);
  if (m) {
    try {
      if (iconv.encodingExists(m[1])) return iconv.decode(buf, m[1]);
    } catch {
      // 回退
    }
  }
  return buf.toString("utf-8");
}
