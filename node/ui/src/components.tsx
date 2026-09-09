// 表单与布局控件（书卷纸感设计系统）：分节卡片/开关/下拉/输入框/单选卡。
import React from "react";
import { AlertTriangle, Check } from "./icons";

export function Button({ variant = "primary", size = "md", className = "", ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger" | "soft"; size?: "sm" | "md" | "lg" }) {
  const base = "inline-flex items-center justify-center gap-1.5 rounded-md font-medium transition-all disabled:opacity-45 disabled:cursor-not-allowed focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-seal-500";
  const variants = {
    primary: "bg-seal-600 text-paper-50 hover:bg-seal-700 active:scale-[.98] shadow-card",
    secondary: "border border-ink-300/70 bg-paper-50 text-ink-600 hover:border-ink-400 hover:text-ink-800 active:scale-[.98] dark:border-ink-700 dark:bg-ink-900 dark:text-ink-200 dark:hover:border-ink-600",
    ghost: "text-ink-500 hover:bg-ink-100 hover:text-ink-800 dark:text-ink-300 dark:hover:bg-ink-800",
    danger: "bg-[#a83232] text-paper-50 hover:bg-[#922a2a]",
    soft: "bg-seal-50 text-seal-700 hover:bg-seal-100 dark:bg-seal-500/15 dark:text-seal-300 dark:hover:bg-seal-500/25",
  };
  const sizes = { sm: "h-8 px-3 text-xs", md: "h-9 px-4 text-sm", lg: "h-11 px-5 text-sm" };
  return <button className={`${base} ${variants[variant]} ${sizes[size]} ${className}`} {...props} />;
}

export function Card({ className = "", children, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={`rounded-lg border border-ink-200/70 bg-paper-50 shadow-card dark:border-ink-800 dark:bg-ink-900 ${className}`} {...rest}>{children}</div>;
}

export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`animate-pulse rounded bg-ink-200/70 dark:bg-ink-800 ${className}`} aria-hidden />;
}

export function SkeletonList({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-4 p-5" role="status" aria-label="加载中">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-4">
          <Skeleton className="h-10 w-10 rounded-lg" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3.5 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </div>
      ))}
    </div>
  );
}

export function EmptyState({ icon, title, hint, action }: { icon?: React.ReactNode; title: string; hint?: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-14 text-center">
      <div className="flex h-14 w-14 items-center justify-center rounded-full border border-ink-200 bg-paper-100 text-ink-400 dark:border-ink-700 dark:bg-ink-800 dark:text-ink-400" aria-hidden>
        {icon ?? <InboxFallback />}
      </div>
      <p className="font-serif text-[15px] text-ink-700 dark:text-ink-100">{title}</p>
      {hint && <p className="max-w-sm text-xs leading-relaxed text-ink-400">{hint}</p>}
      {action}
    </div>
  );
}

function InboxFallback() {
  return (
    <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <rect x="4" y="4" width="16" height="16" rx="2" /><path d="M4 10h4l2 3h4l2-3h4" />
    </svg>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-12 text-center" role="alert">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-seal-50 text-seal-600 dark:bg-seal-500/15 dark:text-seal-300" aria-hidden>
        <AlertTriangle size={20} />
      </div>
      <p className="max-w-md text-sm text-seal-700 dark:text-seal-300">{message}</p>
      {onRetry && <Button variant="secondary" size="sm" onClick={onRetry}>重试</Button>}
    </div>
  );
}

export function Progress({ done, total, label }: { done: number; total: number; label?: string }) {
  const pct = total > 0 ? Math.round((done / total) * 100) : null;
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2 text-xs">
        <span className="truncate text-ink-500 dark:text-ink-300">{label ?? ""}</span>
        <span className="shrink-0 tabular-nums text-ink-400">{pct != null ? `${done}/${total} · ${pct}%` : "…"}</span>
      </div>
      <div className="h-1 w-full overflow-hidden rounded-full bg-ink-200/70 dark:bg-ink-800" role="progressbar" aria-valuenow={pct ?? undefined} aria-valuemin={0} aria-valuemax={100}>
        {pct != null ? (
          <div className="h-full rounded-full bg-seal-500 transition-all duration-500" style={{ width: `${pct}%` }} />
        ) : (
          <div className="h-full w-1/3 animate-pulse rounded-full bg-seal-400" />
        )}
      </div>
    </div>
  );
}

export function Badge({ tone = "gray", children }: { tone?: "gray" | "green" | "amber" | "red" | "blue" | "brand"; children: React.ReactNode }) {
  const tones = {
    gray: "border border-ink-200 bg-transparent text-ink-500 dark:border-ink-700 dark:text-ink-300",
    green: "bg-moss-50 text-moss-700 dark:bg-moss-500/20 dark:text-moss-100",
    amber: "bg-[#f7efdd] text-[#8a6116] dark:bg-[#3d3317] dark:text-[#d8b45e]",
    red: "bg-seal-50 text-seal-700 dark:bg-seal-500/20 dark:text-seal-300",
    blue: "bg-[#eef1f5] text-[#3f5e7e] dark:bg-[#22303f] dark:text-[#9cc0e0]",
    brand: "bg-seal-50 text-seal-700 dark:bg-seal-500/20 dark:text-seal-300",
  };
  return <span className={`inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium ${tones[tone]}`}>{children}</span>;
}

export function Spinner({ className = "" }: { className?: string }) {
  return (
    <svg className={`h-4 w-4 animate-spin ${className}`} viewBox="0 0 24 24" fill="none" aria-label="加载中" role="status">
      <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
      <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
    </svg>
  );
}

// ---- 表单控件 ----

export function Section({ icon, title, desc, children, defaultOpen = true, collapsible = false }: {
  icon?: React.ReactNode; title: string; desc?: string; children: React.ReactNode; defaultOpen?: boolean; collapsible?: boolean;
}) {
  const [open, setOpen] = React.useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <button type="button" className="flex w-full items-center gap-3 px-5 py-4 text-left" disabled={!collapsible}
        onClick={() => collapsible && setOpen(!open)} aria-expanded={open}>
        {icon && <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-ink-200/80 bg-paper-100 text-ink-500 dark:border-ink-700 dark:bg-ink-800 dark:text-ink-300" aria-hidden>{icon}</span>}
        <div className="min-w-0 flex-1">
          <h3 className="font-serif text-[15px] font-semibold text-ink-800 dark:text-paper-100">{title}</h3>
          {desc && <p className="mt-0.5 text-xs text-ink-400">{desc}</p>}
        </div>
        {collapsible && <span className={`text-ink-400 transition-transform ${open ? "rotate-180" : ""}`} aria-hidden>⌄</span>}
      </button>
      {open && <div className="border-t border-ink-200/70 px-5 py-4 dark:border-ink-800">{children}</div>}
    </Card>
  );
}

export function Field({ label, hint, children, required }: { label: string; hint?: string; children: React.ReactNode; required?: boolean }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-baseline gap-1 text-xs font-medium text-ink-600 dark:text-ink-200">
        {label}{required && <span className="text-seal-600">*</span>}
      </span>
      {children}
      {hint && <span className="mt-1 block text-xs leading-relaxed text-ink-400">{hint}</span>}
    </label>
  );
}

const inputCls = "w-full rounded-md border border-ink-200 bg-paper-50 px-3 py-2 text-sm text-ink-800 placeholder-ink-300 transition-colors focus:border-seal-500 focus:outline-none focus:ring-2 focus:ring-seal-500/15 dark:border-ink-700 dark:bg-ink-950 dark:text-ink-100";

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={`${inputCls} ${props.className ?? ""}`} />;
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={`${inputCls} cursor-pointer pr-9 ${props.className ?? ""}`} />;
}

export function Toggle({ checked, onChange, label, desc, disabled }: {
  checked: boolean; onChange: (v: boolean) => void; label: string; desc?: string; disabled?: boolean;
}) {
  return (
    <div className="flex cursor-pointer items-start justify-between gap-4 py-2.5" role="switch" aria-checked={checked} tabIndex={0}
      onClick={() => !disabled && onChange(!checked)}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); !disabled && onChange(!checked); } }}>
      <div className="min-w-0">
        <p className="text-sm font-medium text-ink-700 dark:text-ink-100">{label}</p>
        {desc && <p className="mt-0.5 text-xs leading-relaxed text-ink-400">{desc}</p>}
      </div>
      <span className={`relative mt-0.5 inline-flex h-5 w-10 shrink-0 items-center rounded-full transition-colors ${checked ? "bg-seal-600" : "bg-ink-300 dark:bg-ink-700"} ${disabled ? "opacity-50" : ""}`}>
        <span className={`inline-block h-4 w-4 transform rounded-full bg-paper-50 shadow transition-transform ${checked ? "translate-x-5" : "translate-x-0.5"}`} />
      </span>
    </div>
  );
}

export function RadioCard({ icon, title, desc, active, onClick, badge }: {
  icon?: React.ReactNode; title: string; desc?: string; active: boolean; onClick: () => void; badge?: string;
}) {
  return (
    <button type="button" onClick={onClick} aria-pressed={active}
      className={`relative flex w-full flex-col items-start gap-1 rounded-lg border p-3.5 text-left transition-all ${active
        ? "border-seal-500 bg-seal-50/60 ring-1 ring-seal-500/30 dark:bg-seal-500/10"
        : "border-ink-200/80 bg-paper-50 hover:border-ink-300 dark:border-ink-700 dark:bg-ink-900 dark:hover:border-ink-600"}`}>
      <span className="flex items-center gap-2 text-sm font-medium text-ink-800 dark:text-paper-100">
        {icon && <span aria-hidden>{icon}</span>}{title}
        {badge && <Badge tone="brand">{badge}</Badge>}
      </span>
      {desc && <span className="text-xs leading-relaxed text-ink-400">{desc}</span>}
      {active && <span className="absolute right-3 top-3 flex h-5 w-5 items-center justify-center rounded-full bg-seal-600 text-paper-50" aria-hidden><Check size={11} /></span>}
    </button>
  );
}
