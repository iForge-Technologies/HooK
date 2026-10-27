import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";

type ButtonProps = React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "sm" | "md";
  loading?: boolean;
};

export function Button({ variant = "secondary", size = "md", loading, className = "", children, disabled, ...rest }: ButtonProps) {
  const v = {
    primary: "bg-acc text-acc-ink hover:brightness-95 font-semibold",
    secondary: "bg-surface-2 text-fg border border-line-strong hover:bg-surface-3",
    ghost: "text-fg-2 hover:text-fg hover:bg-surface-2",
    danger: "bg-down text-white hover:brightness-110 font-semibold",
  }[variant];
  const s = size === "sm" ? "h-8 px-3 text-[13px] rounded-[10px] gap-1.5" : "h-10 px-4 text-sm rounded-xl gap-2";
  return (
    <button
      type="button"
      disabled={disabled || loading}
      className={`inline-flex shrink-0 items-center justify-center whitespace-nowrap font-medium tracking-[-0.01em] outline-none transition focus-visible:ring-2 focus-visible:ring-series focus-visible:ring-offset-2 focus-visible:ring-offset-bg disabled:cursor-not-allowed disabled:opacity-50 ${v} ${s} ${className}`}
      {...rest}
    >
      {loading && <Spinner />}
      {children}
    </button>
  );
}

export function Spinner({ className = "size-3.5" }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" className={`animate-spin ${className}`} aria-hidden>
      <circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeOpacity=".25" strokeWidth="2" />
      <path d="M14 8a6 6 0 0 0-6-6" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
    </svg>
  );
}

export function Card({ className = "", flush, children, ...rest }: React.HTMLAttributes<HTMLElement> & { flush?: boolean }) {
  return (
    <section className={`relative min-w-0 rounded-[24px] border border-line bg-surface ${flush ? "overflow-hidden" : "p-5 sm:p-6"} ${className}`} {...rest}>
      {children}
    </section>
  );
}

export function CardTitle({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
      <h2 className="text-[15px] font-semibold tracking-[-0.02em]">{children}</h2>
      {aside}
    </div>
  );
}

export function Field({ label, hint, error, children, htmlFor }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; htmlFor?: string }) {
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <label htmlFor={htmlFor} className="text-[13px] font-medium text-fg-2">
        {label}
      </label>
      {children}
      {error ? <p className="text-xs text-down">{error}</p> : hint ? <p className="text-xs leading-relaxed text-fg-3">{hint}</p> : null}
    </div>
  );
}

const inputCls =
  "h-10 w-full min-w-0 rounded-xl border border-line-strong bg-surface-2 px-3 text-sm text-fg outline-none transition placeholder:text-fg-3 focus:border-series focus:ring-2 focus:ring-series/30";

export function Input({ className = "", ...rest }: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input className={`${inputCls} ${className}`} {...rest} />;
}

export function Textarea({ className = "", ...rest }: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea className={`${inputCls} h-auto min-h-24 py-2.5 font-mono text-[13px] ${className}`} {...rest} />;
}

export function Select({ className = "", children, ...rest }: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select className={`${inputCls} appearance-none bg-[length:12px] bg-[right_12px_center] bg-no-repeat pr-8 ${className}`} style={{ backgroundImage: `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 12 12'%3E%3Cpath d='M3 4.5l3 3 3-3' fill='none' stroke='%23888' stroke-width='1.5' stroke-linecap='round'/%3E%3C/svg%3E")` }} {...rest}>
      {children}
    </select>
  );
}

export function Toggle({ checked, onChange, label, hint }: { checked: boolean; onChange: (v: boolean) => void; label: string; hint?: string }) {
  const id = useId();
  return (
    <div className="flex items-start gap-3">
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative mt-0.5 h-[22px] w-[38px] shrink-0 rounded-full outline-none transition focus-visible:ring-2 focus-visible:ring-series ${checked ? "bg-series" : "bg-surface-3"}`}
      >
        <span className={`absolute top-[3px] size-4 rounded-full bg-white shadow transition-[left] duration-200 ${checked ? "left-[19px]" : "left-[3px]"}`} />
      </button>
      <label htmlFor={id} className="cursor-pointer text-sm">
        <span className="font-medium">{label}</span>
        {hint && <span className="mt-0.5 block text-xs text-fg-3">{hint}</span>}
      </label>
    </div>
  );
}

/** Segmented control with a sliding thumb (from the analytics bento). */
export function Segmented<T extends string>({ items, value, onChange, label }: { items: { value: T; label: string }[]; value: T; onChange: (v: T) => void; label: string }) {
  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const [thumb, setThumb] = useState({ x: 0, w: 0 });
  const box = useRef<HTMLDivElement>(null);
  const idx = Math.max(0, items.findIndex((i) => i.value === value));
  useLayoutEffect(() => {
    const measure = () => {
      const b = refs.current[idx];
      if (b) setThumb((t) => (t.x === b.offsetLeft && t.w === b.offsetWidth ? t : { x: b.offsetLeft, w: b.offsetWidth }));
    };
    measure();
    // fonts loading or the row wrapping changes button widths after first paint
    const ro = new ResizeObserver(measure);
    if (box.current) ro.observe(box.current);
    return () => ro.disconnect();
  }, [idx, items]);
  return (
    <div ref={box} role="radiogroup" aria-label={label} className="relative inline-grid h-8 max-w-full auto-cols-fr grid-flow-col rounded-[11px] border border-line bg-surface-2 p-[2px]">
      <div className="absolute bottom-[2px] top-[2px] rounded-[9px] bg-surface-3 shadow-sm ring-1 ring-line-strong transition-[transform,width] duration-300 [transition-timing-function:cubic-bezier(.2,.9,.25,1)]" style={{ width: thumb.w, transform: `translateX(${thumb.x - 2}px)`, left: 2 }} />
      {items.map((it, i) => (
        <button
          key={it.value}
          ref={(el) => {
            refs.current[i] = el;
          }}
          role="radio"
          type="button"
          aria-checked={i === idx}
          onClick={() => onChange(it.value)}
          className={`relative z-10 whitespace-nowrap rounded-[9px] px-2.5 text-xs font-medium outline-none sm:px-3 transition-colors focus-visible:ring-2 focus-visible:ring-series ${i === idx ? "text-fg" : "text-fg-2 hover:text-fg"}`}
        >
          {it.label}
        </button>
      ))}
    </div>
  );
}

export function Chip({ children, className = "" }: { children: ReactNode; className?: string }) {
  return <span className={`inline-flex h-6 items-center rounded-lg border border-line bg-surface-2 px-2 text-xs font-medium text-fg-2 ${className}`}>{children}</span>;
}

export function ErrorNote({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <div role="alert" className="rounded-xl border border-down/40 bg-down/10 px-3.5 py-2.5 text-sm text-fg">
      {children}
    </div>
  );
}

export function Empty({ title, children, action }: { title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-14 text-center">
      <DotMark className="mb-5 size-11 opacity-80" />
      <h3 className="text-base font-semibold tracking-[-0.02em]">{title}</h3>
      {children && <p className="mt-1.5 max-w-sm text-sm text-fg-2">{children}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

/** The Hook mark: an "H" drawn in dots, after the bento's dot-matrix numbers. */
export function DotMark({ className = "size-7" }: { className?: string }) {
  const on = new Set(["0,0", "0,1", "0,2", "1,1", "2,0", "2,1", "2,2"]);
  return (
    <svg viewBox="0 0 30 30" className={className} aria-hidden>
      <rect width="30" height="30" rx="9" className="fill-fg" />
      {[0, 1, 2].flatMap((x) =>
        [0, 1, 2].map((y) => (
          <circle key={`${x}${y}`} cx={8 + x * 7} cy={8 + y * 7} r="2.3" className={on.has(`${x},${y}`) ? "fill-bg" : "fill-bg/20"} />
        )),
      )}
    </svg>
  );
}
