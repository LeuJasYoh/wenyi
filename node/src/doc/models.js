// 数据模型（主规格 §6 / 分册 03 §1.2）。字段顺序即 JSON 序列化顺序。
export const KIND_TEXT = "text";
export const KIND_HEADING = "heading";

export class Segment {
  constructor({ index, source, kind = KIND_TEXT, target = null, anchor = null, resource_href = null, cont = false, meta = {} }) {
    this.index = index;
    this.source = source;
    this.kind = kind;
    this.target = target;
    this.anchor = anchor;
    this.resource_href = resource_href;
    this.cont = cont;
    this.meta = meta;
  }

  static fromDict(d) {
    return new Segment({
      index: d.index,
      source: d.source,
      kind: d.kind ?? KIND_TEXT,
      target: d.target ?? null,
      anchor: d.anchor ?? null,
      resource_href: d.resource_href ?? null,
      cont: d.cont ?? false,
      meta: d.meta ?? {},
    });
  }

  toDict() {
    return {
      index: this.index,
      source: this.source,
      kind: this.kind,
      target: this.target,
      anchor: this.anchor,
      resource_href: this.resource_href,
      cont: this.cont,
      meta: this.meta,
    };
  }
}

export class Chapter {
  constructor({ index, title = "", segments = [], href = null, template = null, meta = {} }) {
    this.index = index;
    this.title = title;
    this.segments = segments;
    this.href = href;
    this.template = template;
    this.meta = meta;
  }

  /** 需要送翻译的非空 Segment（heading 若 source 非空也包含）。 */
  get text_segments() {
    return this.segments.filter((s) => s.source.trim());
  }

  static fromDict(d) {
    return new Chapter({
      index: d.index,
      title: d.title ?? "",
      segments: (d.segments ?? []).map(Segment.fromDict),
      href: d.href ?? null,
      template: d.template ?? null,
      meta: d.meta ?? {},
    });
  }

  toDict() {
    return {
      index: this.index,
      title: this.title,
      segments: this.segments.map((s) => s.toDict()),
      href: this.href,
      template: this.template,
      meta: this.meta,
    };
  }
}

export class Document {
  constructor({ title = "", source_lang, target_lang, fmt, source_path = "", chapters = [], meta = {} }) {
    this.title = title;
    this.source_lang = source_lang;
    this.target_lang = target_lang;
    this.fmt = fmt;
    this.source_path = source_path;
    this.chapters = chapters;
    this.meta = meta;
  }

  static fromDict(d) {
    return new Document({
      title: d.title ?? "",
      source_lang: d.source_lang,
      target_lang: d.target_lang,
      fmt: d.fmt,
      source_path: d.source_path ?? "",
      chapters: (d.chapters ?? []).map(Chapter.fromDict),
      meta: d.meta ?? {},
    });
  }

  toDict() {
    return {
      title: this.title,
      source_lang: this.source_lang,
      target_lang: this.target_lang,
      fmt: this.fmt,
      source_path: this.source_path,
      chapters: this.chapters.map((c) => c.toDict()),
      meta: this.meta,
    };
  }
}
