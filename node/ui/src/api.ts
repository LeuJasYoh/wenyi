/** API 客户端：REST + SSE（分册 10 契约） */

export interface BookItem {
  slug: string;
  title: string;
  fmt: string | null;
  sourceLang: string | null;
  targetLang: string | null;
  chaptersTotal: number;
  chaptersDone: number;
  status: "idle" | "prepared" | "translating" | "done";
  updatedAt: string | null;
}

export interface Job {
  id: string;
  kind: string;
  book: string;
  status: "running" | "succeeded" | "failed" | "cancelled";
  progress: { done: number; total: number; label: string } | null;
  exitCode: number | null;
  result: any;
  createdAt: string;
  finishedAt: string | null;
  logTail?: string;
}

export interface GlossaryTerm {
  source: string;
  target: string;
  reading: string;
  type: string;
  gender: string;
  aliases: string[];
  first_chapter: number | null;
  note: string;
  status: string;
  updated_at: number;
}

export interface Conflict {
  id: number;
  source: string;
  existing_target: string;
  proposed_target: string;
  chapter: number | null;
  note: string;
  resolved: number;
  created_at: number;
}

async function req<T = any>(method: string, url: string, body?: any): Promise<T> {
  const res = await fetch(`/api/v1${url}`, {
    method,
    headers: body != null ? { "content-type": "application/json" } : undefined,
    body: body != null ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const err: any = new Error(data?.error?.message ?? `HTTP ${res.status}`);
    err.code = data?.error?.code;
    err.details = data?.error?.details;
    throw err;
  }
  return data as T;
}

export const api = {
  health: () => req("GET", "/health"),
  meta: () => req("GET", "/meta"),
  books: (q?: string) => req<{ items: BookItem[]; total: number }>("GET", `/books${q ? `?q=${encodeURIComponent(q)}` : ""}`),
  book: (slug: string) => req("GET", `/books/${slug}`),
  bookStatus: (slug: string) => req("GET", `/books/${slug}/status`),
  chapters: (slug: string) => req("GET", `/books/${slug}/chapters`),
  chapter: (slug: string, n: number) => req("GET", `/books/${slug}/chapters/${n}`),
  prepare: (slug: string, sourcePath?: string) => req<{ job: Job }>("POST", `/books/${slug}/prepare`, sourcePath ? { sourcePath } : {}),
  translate: (slug: string, body: any = {}) => req<{ job: Job }>("POST", `/books/${slug}/translate`, body),
  review: (slug: string) => req<{ job: Job }>("POST", `/books/${slug}/review`, {}),
  qa: (slug: string) => req<{ job: Job }>("POST", `/books/${slug}/qa`, {}),
  report: (slug: string) => req<{ job: Job }>("POST", `/books/${slug}/report`, {}),
  assemble: (slug: string, body: any = {}) => req<{ job: Job }>("POST", `/books/${slug}/assemble`, body),
  jobs: (book?: string) => req<{ items: Job[] }>("GET", `/jobs${book ? `?book=${encodeURIComponent(book)}` : ""}`),
  job: (id: string) => req<{ job: Job }>("GET", `/jobs/${id}`),
  cancel: (id: string) => req<{ job: Job }>("POST", `/jobs/${id}/cancel`),
  glossary: (slug: string, q?: string) => req<{ items: GlossaryTerm[]; stats: { terms: number; open_conflicts: number } }>("GET", `/books/${slug}/glossary${q ? `?q=${encodeURIComponent(q)}` : ""}`),
  conflicts: (slug: string) => req<{ items: Conflict[] }>("GET", `/books/${slug}/glossary/conflicts`),
  resolve: (slug: string, source: string, target: string) => req("POST", `/books/${slug}/glossary/conflicts/resolve`, { source, target }),
  reviews: (slug: string) => req<{ items: any[] }>("GET", `/books/${slug}/reviews`),
  reviewResult: (slug: string, id: string) => req("GET", `/books/${slug}/reviews/${id}`),
  reportJson: (slug: string) => req("GET", `/books/${slug}/report`),
  usage: (slug: string) => req("GET", `/books/${slug}/usage`),
  exports: (slug: string) => req<{ items: any[] }>("GET", `/books/${slug}/exports`),
  events: (slug: string, cursor = 0, limit = 500) => req("GET", `/books/${slug}/events?cursor=${cursor}&limit=${limit}`),
  config: () => req("GET", "/config"),
  saveConfig: (content: string) => req("PUT", "/config", { content }),
  upload: async (file: File) => {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch("/api/v1/uploads", { method: "POST", body: form });
    const data = await res.json();
    if (!res.ok) throw new Error(data?.error?.message ?? "上传失败");
    return data as { sourcePath: string; slug: string; size: number; ext: string };
  },
};

/** 订阅 Job SSE 流（§2.1）。返回取消函数。 */
export function subscribeJob(jobId: string, handlers: {
  onStatus?: (data: any) => void;
  onProgress?: (data: { done: number; total: number; label: string }) => void;
  onLog?: (data: { chunk: string }) => void;
}): () => void {
  const es = new EventSource(`/api/v1/jobs/${jobId}/events`);
  es.addEventListener("job.status", (e) => handlers.onStatus?.(JSON.parse((e as MessageEvent).data)));
  es.addEventListener("job.progress", (e) => handlers.onProgress?.(JSON.parse((e as MessageEvent).data)));
  es.addEventListener("job.log", (e) => handlers.onLog?.(JSON.parse((e as MessageEvent).data)));
  return () => es.close();
}

/** 订阅书级 SSE 流（§2.1：engine.event / state.changed）。 */
export function subscribeBook(slug: string, onEvent: (event: any) => void): () => void {
  const es = new EventSource(`/api/v1/books/${slug}/events/stream`);
  es.addEventListener("engine.event", (e) => onEvent(JSON.parse((e as MessageEvent).data)));
  return () => es.close();
}
