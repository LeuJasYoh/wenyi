#!/usr/bin/env node
// 生成语言无关测试夹具（迁移自 tests/sample_data.py 的样例内容，照抄原始内容）。
// 产物写入 wenyi-multi/testdata/，Go/Node 测试共用。用法: node scripts/gen-fixtures.mjs
import { writeFileSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import JSZip from "jszip";

const OUT = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "testdata");
mkdirSync(OUT, { recursive: true });

export const SAMPLE_TXT = `\
# 第一章　出会い

綾小路は教室の窓際に座っていた。空はどこまでも青く、遠くで鳥が鳴いていた。

「おはよう、綾小路くん」と堀北が声をかけた。彼女はいつも通り無表情だった。

綾小路は小さく頷いた。何も言わなかった。

# 第二章　放課後

放課後、二人は屋上で待ち合わせた。風が強かった。

「先輩、これからどうするつもりですか」と堀北が尋ねた。
`;

const _CONTAINER = `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`;

const _OPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>サンプル小説</dc:title>
    <dc:language>ja</dc:language>
  </metadata>
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="ch2" href="ch2.xhtml" media-type="application/xhtml+xml"/>
  </manifest>
  <spine>
    <itemref idref="ch1"/>
    <itemref idref="ch2"/>
  </spine>
</package>
`;

const _CH1 = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>第一章</title></head>
<body>
<h1>第一章　出会い</h1>
<p>綾小路は教室の窓際に座っていた。</p>
<p>「おはよう、綾小路くん」と堀北が声をかけた。</p>
</body></html>
`;

const _CH2 = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>第二章</title></head>
<body>
<h1>第二章　放課後</h1>
<p>放課後、二人は屋上で待ち合わせた。風が強かった。</p>
</body></html>
`;

const _NESTED_BODY = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Nested</title></head>
<body>
<h1 id="part-1">PART I</h1><p>Part I intro.</p>
<h2 id="section-1">Section 1</h2><p>Section 1 body.</p>
<h1 id="part-2">PART II</h1><p>Part II intro.</p>
<h2 id="section-2">Section 2</h2><p>Section 2 body.</p>
</body></html>
`;

const _NESTED_NCX = `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint id="part1"><navLabel><text>PART I</text></navLabel>
    <content src="body.xhtml#part-1"/>
    <navPoint id="section1"><navLabel><text>Section 1</text></navLabel>
      <content src="body.xhtml#section-1"/>
    </navPoint>
  </navPoint>
  <navPoint id="part2"><navLabel><text>PART II</text></navLabel>
    <content src="body.xhtml#part-2"/>
    <navPoint id="section2"><navLabel><text>Section 2</text></navLabel>
      <content src="body.xhtml#section-2"/>
    </navPoint>
  </navPoint>
</navMap></ncx>
`;

const _FLAT_SECONDARY_NCX = `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint><navLabel><text>PART I</text></navLabel><content src="body.xhtml#part-1"/></navPoint>
  <navPoint><navLabel><text>Section 1</text></navLabel><content src="body.xhtml#section-1"/></navPoint>
  <navPoint><navLabel><text>PART II</text></navLabel><content src="body.xhtml#part-2"/></navPoint>
  <navPoint><navLabel><text>Section 2</text></navLabel><content src="body.xhtml#section-2"/></navPoint>
</navMap></ncx>
`;

const _NESTED_NAV = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"
      xmlns:epub="http://www.idpf.org/2007/ops"><body>
<nav epub:type="toc"><ol>
  <li id="part1"><a href="body.xhtml#part-1">PART I</a><ol>
    <li id="section1"><a href="body.xhtml#section-1">Section 1</a></li>
  </ol></li>
  <li id="part2"><a href="body.xhtml#part-2">PART II</a><ol>
    <li id="section2"><a href="body.xhtml#section-2">Section 2</a></li>
  </ol></li>
</ol></nav></body></html>
`;

async function writeZip(path, entries, binaryEntries = {}) {
  const zip = new JSZip();
  // mimetype 必须最先写且不压缩
  zip.file("mimetype", "application/epub+zip", { compression: "STORE" });
  for (const [name, content] of Object.entries(entries)) {
    zip.file(name, content, { compression: "DEFLATE" });
  }
  for (const [name, content] of Object.entries(binaryEntries)) {
    zip.file(name, content, { compression: "DEFLATE" });
  }
  const buf = await zip.generateAsync({
    type: "nodebuffer",
    compressionOptions: { level: 6 },
    // 保持插入顺序（mimetype 已在最前）
    platform: "UNIX",
  });
  writeFileSync(path, buf);
  console.log(`wrote ${path}`);
}

export function nestedTocOpf({ tocKind = "ncx", ncxFilename = "toc.ncx", navInSpine = false, emptyTitlePage = false } = {}) {
  let tocItem;
  if (tocKind === "ncx") {
    tocItem = `<item id="toc" href="${ncxFilename}" media-type="application/x-dtbncx+xml"/>`;
  } else if (tocKind === "nav") {
    tocItem = `<item id="toc" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`;
  } else {
    tocItem = `<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>` +
      `<item id="toc" href="${ncxFilename}" media-type="application/x-dtbncx+xml"/>`;
  }
  const spineAttr = tocKind === "ncx" || tocKind === "both" ? ` toc="toc"` : "";
  const navSpineId = tocKind === "nav" ? "toc" : "nav";
  const navItemref = navInSpine && (tocKind === "nav" || tocKind === "both") ? `<itemref idref="${navSpineId}"/>` : "";
  const titleItem = emptyTitlePage ? `<item id="title" href="title.xhtml" media-type="application/xhtml+xml"/>` : "";
  const titleItemref = emptyTitlePage ? `<itemref idref="title"/>` : "";
  return `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Nested</dc:title></metadata>
<manifest>${tocItem}${titleItem}<item id="body" href="body.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine${spineAttr}>${navItemref}${titleItemref}<itemref idref="body"/></spine></package>`;
}

export function nestedEntries({ tocKind = "ncx", ncxFilename = "toc.ncx", brokenPart2Fragment = false, navInSpine = false, emptyTitlePage = false } = {}) {
  let ncx = brokenPart2Fragment ? _NESTED_NCX.replaceAll("#part-2", "#missing") : _NESTED_NCX;
  let nav = brokenPart2Fragment ? _NESTED_NAV.replaceAll("#part-2", "#missing") : _NESTED_NAV;
  const entries = {
    "META-INF/container.xml": _CONTAINER,
    "OEBPS/content.opf": nestedTocOpf({ tocKind, ncxFilename, navInSpine, emptyTitlePage }),
    "OEBPS/body.xhtml": _NESTED_BODY,
  };
  if (emptyTitlePage) {
    ncx = ncx.replace(
      "<navMap>",
      "<navMap><navPoint><navLabel><text>Title Page</text></navLabel>" +
        '<content src="title.xhtml"/></navPoint>',
    );
    nav = nav.replace(
      '<nav epub:type="toc"><ol>',
      '<nav epub:type="toc"><ol><li><a href="title.xhtml">Title Page</a></li>',
    );
    entries["OEBPS/title.xhtml"] = '<html><body><div class="cover"></div></body></html>';
  }
  if (tocKind === "ncx") {
    entries[`OEBPS/${ncxFilename}`] = ncx;
  } else if (tocKind === "nav") {
    entries["OEBPS/nav.xhtml"] = nav;
  } else {
    entries["OEBPS/nav.xhtml"] = nav;
    entries[`OEBPS/${ncxFilename}`] = _FLAT_SECONDARY_NCX;
  }
  return entries;
}

export const GROUPED_NAV_ENTRIES = {
  "META-INF/container.xml": _CONTAINER,
  "OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Grouped</dc:title></metadata>
<manifest>
  <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
  <item id="body" href="body.xhtml" media-type="application/xhtml+xml"/>
</manifest><spine><itemref idref="body"/></spine></package>`,
  "OEBPS/nav.xhtml": `<html xmlns="http://www.w3.org/1999/xhtml"
 xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol>
 <li><span>PART I</span><ol><li><a href="body.xhtml#section-1">Section 1</a></li></ol></li>
 <li><span>PART II</span><ol><li><a href="body.xhtml#section-2">Section 2</a></li></ol></li>
</ol></nav></body></html>`,
  "OEBPS/body.xhtml": `<html><body>
<h2 id="section-1">Section 1</h2><p>One.</p>
<h2 id="section-2">Section 2</h2><p>Two.</p>
</body></html>`,
};

export const CROSS_RESOURCE_ENTRIES = {
  "META-INF/container.xml": _CONTAINER,
  "OEBPS/content.opf": `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Cross</dc:title></metadata>
<manifest>
  <item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
  <item id="one" href="one.xhtml" media-type="application/xhtml+xml"/>
  <item id="two" href="two.xhtml" media-type="application/xhtml+xml"/>
  <item id="three" href="three.xhtml" media-type="application/xhtml+xml"/>
</manifest>
<spine toc="toc"><itemref idref="one"/><itemref idref="two"/><itemref idref="three"/></spine>
</package>`,
  "OEBPS/toc.ncx": `<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/"><navMap>
  <navPoint><navLabel><text>PART I</text></navLabel><content src="one.xhtml#part-1"/>
    <navPoint><navLabel><text>Section 1</text></navLabel><content src="two.xhtml#section-1"/></navPoint>
  </navPoint>
  <navPoint><navLabel><text>PART II</text></navLabel><content src="three.xhtml#part-2"/></navPoint>
</navMap></ncx>`,
  "OEBPS/one.xhtml": `<html><body><h1 id="part-1">PART I</h1><p>One.</p></body></html>`,
  "OEBPS/two.xhtml": `<html><body><h2 id="section-1">Section 1</h2><p>Two.</p></body></html>`,
  "OEBPS/three.xhtml": `<html><body><h1 id="part-2">PART II</h1><p>Three.</p></body></html>`,
};

const _INLINE_OPF = `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:title>内联插图样本</dc:title>
    <dc:language>fr</dc:language>
  </metadata>
  <manifest>
    <item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/>
    <item id="image" href="image.jpg" media-type="image/jpeg"/>
  </manifest>
  <spine><itemref idref="ch1"/></spine>
</package>
`;

const _INLINE_CH1 = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>Chapitre I</title></head>
<body>
<h1>Chapitre I</h1>
<p class="Textbody"><img src="image.jpg"/><span id="kobo.1.1">Je suis là, sous le pommier.</span></p>
</body></html>
`;

async function mainGen() {
  mkdirSync(join(OUT, "epub"), { recursive: true });
  writeFileSync(join(OUT, "sample.txt"), SAMPLE_TXT, "utf8");
  console.log("wrote sample.txt");

  await writeZip(join(OUT, "epub", "sample.epub"), {
    "META-INF/container.xml": _CONTAINER,
    "OEBPS/content.opf": _OPF,
    "OEBPS/ch1.xhtml": _CH1,
    "OEBPS/ch2.xhtml": _CH2,
  });
  await writeZip(join(OUT, "epub", "nested-ncx.epub"), nestedEntries({ tocKind: "ncx" }));
  await writeZip(join(OUT, "epub", "nested-nav.epub"), nestedEntries({ tocKind: "nav" }));
  await writeZip(join(OUT, "epub", "nested-both.epub"), nestedEntries({ tocKind: "both" }));
  await writeZip(join(OUT, "epub", "nested-both-broken-fragment.epub"), nestedEntries({ tocKind: "both", brokenPart2Fragment: true }));
  await writeZip(join(OUT, "epub", "nested-both-nav-in-spine.epub"), nestedEntries({ tocKind: "both", navInSpine: true }));
  await writeZip(join(OUT, "epub", "nested-both-empty-title-page.epub"), nestedEntries({ tocKind: "both", emptyTitlePage: true }));
  await writeZip(join(OUT, "epub", "nested-ncx-custom-filename.epub"), nestedEntries({ tocKind: "ncx", ncxFilename: "toc.xml" }));
  await writeZip(join(OUT, "epub", "grouped-nav.epub"), GROUPED_NAV_ENTRIES);
  await writeZip(join(OUT, "epub", "cross-resource.epub"), CROSS_RESOURCE_ENTRIES);
  await writeZip(
    join(OUT, "epub", "inline-sample.epub"),
    {
      "META-INF/container.xml": _CONTAINER,
      "OEBPS/content.opf": _INLINE_OPF,
      "OEBPS/ch1.xhtml": _INLINE_CH1,
    },
    { "OEBPS/image.jpg": Buffer.from("inline-image", "utf8") },
  );
}

if (process.argv[1] && process.argv[1].endsWith("gen-fixtures.mjs")) {
  mainGen();
}
