// 表单与布局控件（设计系统扩展）：分节卡片/开关/下拉/输入框/单选卡。
import React from "react";

export function Button({ variant = "primary", size = "md", className = "", ...props }: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "ghost" | "danger" | "soft"; size?: "sm" | "md" | "lg" }) {
  const base = "inline-flex items-center justify-center gap-1.5 rounded-lg font-medium transition-all disabled:opacity-50 disabled:cursor-not-allowed focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-brand-500";
  const variants = {
    primary: "bg-brand-600 text-white hover:bg-brand-700 active:scale-[.98] shadow-sm",
    secondary: "border border-gray-200 bg-white text-gray-700 hover:bg-gray-50 active:scale-[.98] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200 dark:hover:bg-gray-800",
    ghost: "text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800",
    danger: "bg-red-600 text-white hover:bg-red-700",
    soft: "bg-brand-50 text-brand-700 hover:bg-brand-100 dark:bg-brand-900/30 dark:text-brand-300 dark:hover:bg-brand-900/50",
  };
  const sizes = { sm: "h-8 px-3 text-xs", md: "h-9.5 px-4 text-sm", lg: "h-11 px-5 text-sm" };
  return <button className={`${base} ${variants[variant]} ${sizes[size]} ${className}`} {...props} />;
}

export function Card({ className = "", children, ...rest }: React.HTMLAttributes<HTMLDivElement>) {
  return <div className={`rounded-xl border border-gray-200/80 bg-white shadow-sm dark:border-gray-800 dark:bg-gray-900 ${className}`} {...rest}>{children}</div>;
}

export function Skeleton({ className = "" }: { className?: string }) {
  return <div className={`animate-pulse rounded bg-gray-200 dark:bg-gray-700 ${className}`} aria-hidden />;
}

export function SkeletonList({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-4 p-5" role="status" aria-label="加载中">
      {Array.from({ length: rows }).map((_, i) => (
        <div key={i} className="flex items-center gap-4">
          <Skeleton className="h-10 w-10 rounded-xl" />
          <div className="flex-1 space-y-2">
            <Skeleton className="h-3.5 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </div>
      ))}
    </div>
  );
}

export function EmptyState({ icon = "📭", title, hint, action }: { icon?: string; title: string; hint?: string; action?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center gap-2.5 px-6 py-16 text-center">
      <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-gray-100 text-3xl dark:bg-gray-800" aria-hidden>{icon}</div>
      <p className="text-sm font-medium text-gray-800 dark:text-gray-100">{title}</p>
      {hint && <p className="max-w-sm text-xs leading-relaxed text-gray-500 dark:text-gray-400">{hint}</p>}
      {action}
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 px-6 py-12 text-center" role="alert">
      <div className="flex h-14 w-14 items-center justify-center rounded-2xl bg-red-50 text-2xl dark:bg-red-900/30" aria-hidden>⚠️</div>
      <p className="max-w-md text-sm text-red-600 dark:text-red-400">{message}</p>
      {onRetry && <Button variant="secondary" size="sm" onClick={onRetry}>重试</Button>}
    </div>
  );
}

export function Progress({ done, total, label }: { done: number; total: number; label?: string }) {
  const pct = total > 0 ? Math.round((done / total) * 100) : null;
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2 text-xs">
        <span className="truncate text-gray-600 dark:text-gray-300">{label ?? ""}</span>
        <span className="shrink-0 tabular-nums text-gray-400">{pct != null ? `${done}/${total} · ${pct}%` : "…"}</span>
      </div>
      <div className="h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800" role="progressbar" aria-valuenow={pct ?? undefined} aria-valuemin={0} aria-valuemax={100}>
        {pct != null ? (
          <div className="h-full rounded-full bg-gradient-to-r from-brand-500 to-brand-600 transition-all duration-500" style={{ width: `${pct}%` }} />
        ) : (
          <div className="h-full w-1/3 animate-pulse rounded-full bg-brand-500" />
        )}
      </div>
    </div>
  );
}

export function Badge({ tone = "gray", children }: { tone?: "gray" | "green" | "amber" | "red" | "blue" | "brand"; children: React.ReactNode }) {
  const tones = {
    gray: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300",
    green: "bg-emerald-50 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300",
    amber: "bg-amber-50 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300",
    red: "bg-red-50 text-red-700 dark:bg-red-900/40 dark:text-red-300",
    blue: "bg-blue-50 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300",
    brand: "bg-brand-50 text-brand-700 dark:bg-brand-900/40 dark:text-brand-300",
  };
  return <span className={`inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>{children}</span>;
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
  icon?: string; title: string; desc?: string; children: React.ReactNode; defaultOpen?: boolean; collapsible?: boolean;
}) {
  const [open, setOpen] = React.useState(defaultOpen);
  return (
    <Card className="overflow-hidden">
      <button type="button" className="flex w-full items-center gap-3 px-5 py-4 text-left" disabled={!collapsible}
        onClick={() => collapsible && setOpen(!open)} aria-expanded={open}>
        {icon && <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-base dark:bg-brand-900/40" aria-hidden>{icon}</span>}
        <div className="min-w-0 flex-1">
          <h3 className="text-sm font-semibold text-gray-900 dark:text-gray-50">{title}</h3>
          {desc && <p className="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{desc}</p>}
        </div>
        {collapsible && <span className={`text-gray-400 transition-transform ${open ? "rotate-180" : ""}`} aria-hidden>⌄</span>}
      </button>
      {open && <div className="border-t border-gray-100 px-5 py-4 dark:border-gray-800">{children}</div>}
    </Card>
  );
}

export function Field({ label, hint, children, required }: { label: string; hint?: string; children: React.ReactNode; required?: boolean }) {
  return (
    <label className="block">
      <span className="mb-1.5 flex items-baseline gap-1 text-xs font-medium text-gray-700 dark:text-gray-200">
        {label}{required && <span className="text-red-500">*</span>}
      </span>
      {children}
      {hint && <span className="mt-1 block text-xs leading-relaxed text-gray-500 dark:text-gray-400">{hint}</span>}
    </label>
  );
}

const inputCls = "w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm text-gray-900 placeholder-gray-400 transition-colors focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-500/20 dark:border-gray-700 dark:bg-gray-950 dark:text-gray-100";

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
        <p className="text-sm font-medium text-gray-800 dark:text-gray-100">{label}</p>
        {desc && <p className="mt-0.5 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{desc}</p>}
      </div>
      <span className={`relative mt-0.5 inline-flex h-5 w-10 shrink-0 items-center rounded-full transition-colors ${checked ? "bg-brand-600" : "bg-gray-200 dark:bg-gray-700"} ${disabled ? "opacity-50" : ""}`}>
        <span className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${checked ? "translate-x-5" : "translate-x-0.5"}`} />
      </span>
    </div>
  );
}

export function RadioCard({ icon, title, desc, active, onClick, badge }: {
  icon?: string; title: string; desc?: string; active: boolean; onClick: () => void; badge?: string;
}) {
  return (
    <button type="button" onClick={onClick} aria-pressed={active}
      className={`relative flex w-full flex-col items-start gap-1 rounded-xl border p-3.5 text-left transition-all ${active
        ? "border-brand-500 bg-brand-50/50 ring-2 ring-brand-500/20 dark:bg-brand-900/20"
        : "border-gray-200 bg-white hover:border-gray-300 dark:border-gray-700 dark:bg-gray-900 dark:hover:border-gray-600"}`}>
      <span className="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-gray-50">
        {icon && <span aria-hidden>{icon}</span>}{title}
        {badge && <Badge tone="brand">{badge}</Badge>}
      </span>
      {desc && <span className="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{desc}</span>}
      {active && <span className="absolute right-3 top-3 flex h-5 w-5 items-center justify-center rounded-full bg-brand-600 text-[10px] text-white" aria-hidden>✓</span>}
    </button>
  );
}
