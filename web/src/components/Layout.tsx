import { useQueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link, useLocation } from "wouter";
import { api, type AuthStatus } from "../lib/api";
import { useTheme } from "../lib/hooks";
import { useLiveUpdates } from "../lib/live";
import { DotMark } from "./ui";

const NAV = [
  { href: "/", label: "Overview", icon: "M4 4h7v9H4zM13 4h7v5h-7zM13 11h7v9h-7zM4 15h7v5H4z" },
  { href: "/monitors", label: "Monitors", icon: "M3 12h4l2-6 4 12 2-6h6" },
  { href: "/servers", label: "Servers", icon: "M4 5h16v5H4zM4 14h16v5H4zM7.5 7.5h.01M7.5 16.5h.01" },
  { href: "/platforms", label: "Platforms", icon: "M12 3l9 16H3zM12 10v4" },
  { href: "/maintenance", label: "Maintenance", icon: "M14.5 5.5a4 4 0 0 0-5.3 5.1L4 15.8 6.2 18l5.2-5.2a4 4 0 0 0 5.1-5.3l-2.4 2.4-2.1-.5-.5-2.1z" },
  { href: "/status-pages", label: "Status pages", icon: "M4 5h16v11H4zM8 20h8M12 16v4M8 10.5l2.5 2.5L16 8" },
  { href: "/alerts", label: "Alerts", icon: "M6 16V11a6 6 0 1 1 12 0v5l2 2H4zM10 21h4" },
  { href: "/settings", label: "Settings", icon: "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19 12a7 7 0 0 0-.1-1.2l2-1.6-2-3.4-2.4 1a7 7 0 0 0-2-1.2L14 3h-4l-.5 2.6a7 7 0 0 0-2 1.2l-2.4-1-2 3.4 2 1.6a7 7 0 0 0 0 2.4l-2 1.6 2 3.4 2.4-1a7 7 0 0 0 2 1.2L10 21h4l.5-2.6a7 7 0 0 0 2-1.2l2.4 1 2-3.4-2-1.6c.1-.4.1-.8.1-1.2z" },
];

function NavIcon({ d }: { d: string }) {
  return (
    <svg viewBox="0 0 24 24" className="size-[18px] shrink-0" aria-hidden>
      <path d={d} fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Layout({ user, version, children }: { user: NonNullable<AuthStatus["user"]>; version: string; children: ReactNode }) {
  const [loc] = useLocation();
  const qc = useQueryClient();
  const live = useLiveUpdates(true);
  const [theme, setTheme] = useTheme();
  const active = (href: string) => (href === "/" ? loc === "/" : loc.startsWith(href));

  const logout = async () => {
    await api.post("/auth/logout").catch(() => {});
    qc.removeQueries({ predicate: (q) => q.queryKey[0] !== "auth" });
    qc.invalidateQueries({ queryKey: ["auth"] });
  };

  const themeBtn = (
    <button
      type="button"
      onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
      className="grid size-9 place-items-center rounded-xl text-fg-2 hover:bg-surface-2 hover:text-fg"
      aria-label={`Switch to ${theme === "dark" ? "light" : "dark"} theme`}
      title="Toggle theme"
    >
      <svg viewBox="0 0 24 24" className="size-[18px]" aria-hidden>
        {theme === "dark" ? (
          <path d="M12 4V2M12 22v-2M4 12H2M22 12h-2M5.6 5.6 4.2 4.2M19.8 19.8l-1.4-1.4M5.6 18.4l-1.4 1.4M19.8 4.2l-1.4 1.4M12 17a5 5 0 1 0 0-10 5 5 0 0 0 0 10z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        ) : (
          <path d="M20 14.5A8 8 0 0 1 9.5 4 8 8 0 1 0 20 14.5z" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinejoin="round" />
        )}
      </svg>
    </button>
  );

  const liveBadge = (
    <span className="inline-flex items-center gap-1.5 text-xs text-fg-2" title={live ? "Receiving live updates" : "Reconnecting to live updates"}>
      <span className={`size-1.5 rounded-full ${live ? "bg-up" : "bg-warn"}`} />
      {live ? "Live" : "Reconnecting"}
    </span>
  );

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[232px_minmax(0,1fr)]">
      {/* desktop sidebar */}
      <aside className="sticky top-0 hidden h-dvh flex-col border-r border-line px-4 py-5 lg:flex">
        <Link href="/" className="mb-8 flex items-center gap-2.5 px-2">
          <DotMark className="size-8" />
          <span className="text-lg font-semibold tracking-[-0.03em]">Hook</span>
        </Link>
        <nav className="flex flex-col gap-1" aria-label="Main">
          {NAV.map((n) => (
            <Link
              key={n.href}
              href={n.href}
              aria-current={active(n.href) ? "page" : undefined}
              className={`flex h-10 items-center gap-3 rounded-xl px-3 text-sm font-medium transition-colors ${active(n.href) ? "bg-surface-2 text-fg" : "text-fg-2 hover:bg-surface-2/60 hover:text-fg"}`}
            >
              <NavIcon d={n.icon} />
              {n.label}
            </Link>
          ))}
        </nav>
        <div className="mt-auto flex flex-col gap-3 border-t border-line px-2 pt-4">
          <div className="flex items-center justify-between">
            {liveBadge}
            {themeBtn}
          </div>
          <div className="flex items-center justify-between gap-2 text-sm">
            <span className="truncate text-fg-2" title={user.username}>
              {user.username}
            </span>
            <button type="button" onClick={logout} className="text-xs text-fg-3 hover:text-fg">
              Sign out
            </button>
          </div>
          <span className="text-[11px] text-fg-3">v{version}</span>
        </div>
      </aside>

      {/* mobile top bar */}
      <header className="sticky top-0 z-30 border-b border-line bg-bg/85 px-4 pt-[env(safe-area-inset-top)] backdrop-blur-lg lg:hidden">
        <div className="flex h-14 items-center gap-3">
          <Link href="/" className="flex items-center gap-2">
            <DotMark className="size-7" />
            <span className="font-semibold tracking-[-0.03em]">Hook</span>
          </Link>
          <div className="ml-auto flex items-center gap-1">
            {liveBadge}
            {themeBtn}
          </div>
        </div>
        <nav className="-mx-4 flex gap-1 overflow-x-auto px-3 pb-2 [scrollbar-width:none]" aria-label="Main">
          {NAV.map((n) => (
            <Link
              key={n.href}
              href={n.href}
              aria-current={active(n.href) ? "page" : undefined}
              className={`flex h-8 shrink-0 items-center rounded-lg px-3 text-sm font-medium ${active(n.href) ? "bg-surface-2 text-fg" : "text-fg-2"}`}
            >
              {n.label}
            </Link>
          ))}
        </nav>
      </header>

      <main className="mx-auto w-full max-w-[1400px] px-4 pb-16 pt-5 sm:px-6 lg:px-10 lg:pt-8">{children}</main>
    </div>
  );
}
