import { useQuery } from "@tanstack/react-query";
import { Redirect, Route, Switch, useLocation } from "wouter";
import { Layout } from "./components/Layout";
import { DotMark, Spinner } from "./components/ui";
import { api, type AuthStatus } from "./lib/api";
import { AlertsPage } from "./pages/Alerts";
import { AuthScreen } from "./pages/Auth";
import { MonitorDetailPage } from "./pages/MonitorDetail";
import { MonitorFormPage } from "./pages/MonitorForm";
import { MonitorsPage } from "./pages/Monitors";
import { OverviewPage } from "./pages/Overview";
import { PlatformsPage } from "./pages/Platforms";
import { ServerDetailPage } from "./pages/ServerDetail";
import { ServersPage } from "./pages/Servers";
import { SettingsPage } from "./pages/Settings";
import { MaintenancePage } from "./pages/Maintenance";
import { PublicStatusPage } from "./pages/PublicStatus";
import { StatusPagesPage } from "./pages/StatusPages";
import { KioskPage } from "./pages/Kiosk";

export function App() {
  const [loc] = useLocation();
  const publicSlug = loc.startsWith("/status/") ? decodeURIComponent(loc.slice("/status/".length)) : null;
  const kioskToken = loc.startsWith("/kiosk/") ? loc.slice("/kiosk/".length) : null;
  // Public status pages and kiosk boards render without signing in.
  const auth = useQuery({ queryKey: ["auth"], queryFn: () => api.get<AuthStatus>("/auth/status"), staleTime: 60_000, enabled: publicSlug == null && kioskToken == null });

  if (publicSlug != null) return <PublicStatusPage slug={publicSlug} />;
  if (kioskToken != null) return <KioskPage token={kioskToken} />;

  if (auth.isPending) {
    return (
      <div className="grid min-h-dvh place-items-center">
        <div className="flex items-center gap-3 text-fg-2">
          <DotMark className="size-8" />
          <Spinner />
        </div>
      </div>
    );
  }
  if (auth.isError) {
    return (
      <div className="grid min-h-dvh place-items-center px-6 text-center">
        <div>
          <p className="font-semibold">Can't reach the Hook hub.</p>
          <p className="mt-1 text-sm text-fg-2">{(auth.error as Error).message}</p>
          <button className="mt-4 text-sm text-series underline" onClick={() => auth.refetch()}>
            Try again
          </button>
        </div>
      </div>
    );
  }
  if (!auth.data.authenticated) return <AuthScreen status={auth.data} />;

  return (
    <Layout user={auth.data.user!} version={auth.data.version}>
      <Switch>
        <Route path="/" component={OverviewPage} />
        <Route path="/monitors" component={MonitorsPage} />
        <Route path="/monitors/new">{() => <MonitorFormPage />}</Route>
        <Route path="/monitors/:id/edit">{(p) => <MonitorFormPage id={Number(p.id)} />}</Route>
        <Route path="/monitors/:id">{(p) => <MonitorDetailPage id={Number(p.id)} />}</Route>
        <Route path="/servers" component={ServersPage} />
        <Route path="/servers/:id">{(p) => <ServerDetailPage id={Number(p.id)} />}</Route>
        <Route path="/platforms" component={PlatformsPage} />
        <Route path="/maintenance" component={MaintenancePage} />
        <Route path="/status-pages" component={StatusPagesPage} />
        <Route path="/alerts" component={AlertsPage} />
        <Route path="/settings">{() => <SettingsPage user={auth.data.user!} version={auth.data.version} />}</Route>
        <Route>
          <Redirect to="/" />
        </Route>
      </Switch>
    </Layout>
  );
}
