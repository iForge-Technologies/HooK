import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";

/* ---------------- shared tooltip (one per app, like the bento tooltip) ---------------- */

export type TipRow = { label: string; value: string; color?: string; hollow?: boolean };
export type Tip = { title: string; rows: TipRow[]; note?: string };
type TipApi = { show: (x: number, y: number, tip: Tip) => void; hide: () => void };

const TipCtx = createContext<TipApi>({ show: () => {}, hide: () => {} });
export const useTip = () => useContext(TipCtx);

function TooltipLayer({ api }: { api: React.RefObject<TipApi | null> }) {
  const [tip, setTip] = useState<(Tip & { x: number; y: number }) | null>(null);
  const [pos, setPos] = useState({ l: -9999, t: -9999 });
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    api.current = { show: (x, y, t) => setTip({ ...t, x, y }), hide: () => setTip(null) };
  }, [api]);
  useLayoutEffect(() => {
    if (!tip || !box.current) return;
    const r = box.current.getBoundingClientRect();
    let l = tip.x + 14, t = tip.y - r.height - 12;
    if (l + r.width > window.innerWidth - 8) l = tip.x - r.width - 14;
    if (l < 8) l = 8;
    if (t < 8) t = tip.y + 18;
    setPos({ l, t });
  }, [tip]);
  return (
    <div
      ref={box}
      role="tooltip"
      className={`pointer-events-none fixed z-[60] min-w-[150px] max-w-[320px] rounded-xl border border-line-strong bg-surface-2/95 px-3 py-2 text-xs shadow-[0_12px_30px_-10px_rgba(0,0,0,.6)] backdrop-blur-md transition-opacity duration-150 ${tip ? "opacity-100" : "opacity-0"}`}
      style={{ left: pos.l, top: pos.t }}
    >
      {tip && (
        <>
          <div className="mb-1 font-medium text-fg-2">{tip.title}</div>
          {tip.rows.map((r) => (
            <div key={r.label} className="flex items-center justify-between gap-4 leading-5">
              <span className="inline-flex items-center gap-1.5 text-fg-2">
                {r.color && (
                  <i
                    className="inline-block size-2 rounded-full"
                    style={r.hollow ? { boxShadow: `inset 0 0 0 1.5px ${r.color}` } : { background: r.color }}
                  />
                )}
                {r.label}
              </span>
              <b className="tnum font-mono font-medium text-fg">{r.value}</b>
            </div>
          ))}
          {tip.note && <div className="mt-1.5 border-t border-line pt-1.5 whitespace-normal text-fg-2">{tip.note}</div>}
        </>
      )}
    </div>
  );
}

/* ---------------- toasts ---------------- */

type Toast = { id: number; text: string; tone: "ok" | "error" };
const ToastCtx = createContext<(text: string, tone?: Toast["tone"]) => void>(() => {});
export const useToast = () => useContext(ToastCtx);

export function OverlayProvider({ children }: { children: ReactNode }) {
  const tipApi = useRef<TipApi | null>(null);
  const tip = useRef<TipApi>({
    show: (x, y, t) => tipApi.current?.show(x, y, t),
    hide: () => tipApi.current?.hide(),
  }).current;

  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((text: string, tone: Toast["tone"] = "ok") => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t.slice(-3), { id, text, tone }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), tone === "error" ? 6000 : 3200);
  }, []);

  return (
    <TipCtx.Provider value={tip}>
      <ToastCtx.Provider value={push}>
        {children}
        <TooltipLayer api={tipApi} />
        <div className="pointer-events-none fixed inset-x-0 bottom-[max(16px,env(safe-area-inset-bottom))] z-[70] flex flex-col items-center gap-2 px-4" aria-live="polite">
          {toasts.map((t) => (
            <div
              key={t.id}
              className="enter pointer-events-auto flex max-w-md items-center gap-2.5 rounded-2xl border border-line-strong bg-surface-2 px-4 py-2.5 text-sm shadow-[0_16px_40px_-12px_rgba(0,0,0,.7)]"
            >
              <span className={`size-2 shrink-0 rounded-full ${t.tone === "ok" ? "bg-up" : "bg-down"}`} aria-hidden />
              {t.text}
            </div>
          ))}
        </div>
      </ToastCtx.Provider>
    </TipCtx.Provider>
  );
}

/* ---------------- modal ---------------- */

export function Modal({ open, onClose, title, children, width = "max-w-lg" }: { open: boolean; onClose: () => void; title: string; children: ReactNode; width?: string }) {
  const panel = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const prev = document.activeElement as HTMLElement | null;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    panel.current?.querySelector<HTMLElement>("input, select, textarea, button")?.focus();
    document.body.style.overflow = "hidden";
    return () => {
      window.removeEventListener("keydown", onKey);
      document.body.style.overflow = "";
      prev?.focus();
    };
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-black/60 p-0 backdrop-blur-sm sm:items-center sm:p-6" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={panel}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={`enter max-h-[92dvh] w-full ${width} overflow-y-auto rounded-t-[28px] border border-line-strong bg-surface p-5 pb-[max(20px,env(safe-area-inset-bottom))] shadow-2xl sm:rounded-[28px] sm:p-6`}
      >
        <div className="mb-5 flex items-center justify-between gap-4">
          <h2 className="text-lg font-semibold tracking-[-0.02em]">{title}</h2>
          <button type="button" onClick={onClose} className="grid size-8 place-items-center rounded-full text-fg-2 hover:bg-surface-3 hover:text-fg" aria-label="Close">
            <svg viewBox="0 0 16 16" className="size-4" aria-hidden>
              <path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
            </svg>
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
