import { MutationCache, QueryCache, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { OverlayProvider } from "./components/overlay";
import { ApiError } from "./lib/api";
import { readTheme } from "./lib/hooks";
import { registerServiceWorker } from "./lib/push";
import "./index.css";

document.documentElement.classList.toggle("light", readTheme() === "light");
registerServiceWorker();

// Any 401 means the session ended: re-check auth so the login screen shows.
const onError = (err: unknown) => {
  if (err instanceof ApiError && err.status === 401) queryClient.invalidateQueries({ queryKey: ["auth"] });
};
const queryClient: QueryClient = new QueryClient({
  queryCache: new QueryCache({ onError }),
  mutationCache: new MutationCache({ onError }),
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: true,
      retry: (n, err) => !(err instanceof ApiError && err.status < 500) && n < 2,
    },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <OverlayProvider>
        <App />
      </OverlayProvider>
    </QueryClientProvider>
  </StrictMode>,
);
