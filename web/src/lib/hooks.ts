import { useEffect, useRef, useState } from "react";

/** Tracks an element's content size. */
export function useSize<T extends HTMLElement = HTMLDivElement>() {
  const ref = useRef<T | null>(null);
  const [size, setSize] = useState({ w: 0, h: 0 });
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(([e]) => {
      const w = Math.round(e.contentRect.width), h = Math.round(e.contentRect.height);
      setSize((p) => (p.w === w && p.h === h ? p : { w, h }));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, size] as const;
}

/** Re-renders every `ms` so relative times ("12s ago") stay fresh. */
export function useNow(ms = 1000) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(id);
  }, [ms]);
  return now;
}

export function useReducedMotion() {
  const [r, setR] = useState(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  useEffect(() => {
    const m = window.matchMedia("(prefers-reduced-motion: reduce)");
    const on = () => setR(m.matches);
    m.addEventListener("change", on);
    return () => m.removeEventListener("change", on);
  }, []);
  return r;
}

type Theme = "dark" | "light";

export function useTheme() {
  const [theme, setTheme] = useState<Theme>(() => readTheme());
  useEffect(() => {
    document.documentElement.classList.toggle("light", theme === "light");
    document.documentElement.classList.toggle("dark", theme === "dark");
    try {
      localStorage.setItem("hook-theme", theme);
    } catch {
      /* storage blocked: theme just won't persist */
    }
  }, [theme]);
  return [theme, setTheme] as const;
}

export function readTheme(): Theme {
  try {
    return localStorage.getItem("hook-theme") === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}
