import type { ServerDetail } from "../lib/api";
import { StatusIcon } from "./status";
import { Card, CardTitle } from "./ui";

/** Disk-full projection and right-sizing, said plainly with their caveats. */
export function ForecastCard({ forecast }: { forecast: ServerDetail["forecast"] }) {
  const { disk: d, sizing: s } = forecast;
  const days = d.daysToFull;
  const soon = days != null && d.confident && days <= 14;
  return (
    <Card className="lg:col-span-2">
      <CardTitle aside={<span className="text-xs text-fg-3">updated hourly</span>}>Forecast</CardTitle>
      <div className="grid gap-6 md:grid-cols-2">
        <div>
          <div className="text-xs font-medium text-fg-2">Fullest disk</div>
          {days != null && d.confident ? (
            <>
              <div className={`tnum mt-1.5 text-[34px] leading-none font-light tracking-[-0.04em] ${soon ? "text-warn" : ""}`}>
                ~{days < 1 ? "<1" : Math.round(days)} days
              </div>
              <p className="mt-2 text-sm text-fg-2">
                until full, at {d.current.toFixed(0)}% and growing {d.slopePerDay.toFixed(2)} points a day.
              </p>
              <p className="mt-1 text-xs text-fg-3">
                Straight-line projection from {Math.round(d.points / 24)} days of hourly data (fit R² {d.r2.toFixed(2)}). Alerts at 7 days.
              </p>
            </>
          ) : days != null ? (
            <p className="mt-2 text-sm text-fg-2">
              Growing about {d.slopePerDay.toFixed(2)} points a day, but usage is too irregular to project a date reliably (fit R² {d.r2.toFixed(2)}).
            </p>
          ) : d.points < 24 ? (
            <p className="mt-2 text-sm text-fg-2">Needs at least a day of history to project ({d.points} hourly points so far).</p>
          ) : (
            <p className="mt-2 flex items-center gap-2 text-sm text-fg-2">
              <StatusIcon status="up" className="size-4" />
              Not filling up: {d.current.toFixed(0)}% used, {d.slopePerDay >= 0 ? "+" : ""}
              {d.slopePerDay.toFixed(2)} points a day.
            </p>
          )}
        </div>
        <div>
          <div className="text-xs font-medium text-fg-2">Size</div>
          {s.verdict === "" ? (
            <p className="mt-2 text-sm text-fg-2">Needs a week of history to judge ({s.days.toFixed(1)} days so far).</p>
          ) : (
            <>
              <div className={`mt-1.5 text-[22px] font-semibold tracking-[-0.02em] ${s.verdict === "tight" ? "text-warn" : ""}`}>
                {s.verdict === "oversized" ? "Probably oversized" : s.verdict === "tight" ? "Running tight" : "About right"}
              </div>
              <p className="mt-2 text-sm text-fg-2">
                Over {Math.round(s.days)} days CPU peaked at {s.cpuPeak.toFixed(0)}% (95% of hours under {s.cpuP95.toFixed(0)}%) and memory at {s.memPeak.toFixed(0)}%.
                {s.verdict === "oversized" && " A smaller plan would likely cope."}
                {s.verdict === "tight" && " Consider a bigger plan or trimming what runs here."}
              </p>
            </>
          )}
        </div>
      </div>
    </Card>
  );
}
