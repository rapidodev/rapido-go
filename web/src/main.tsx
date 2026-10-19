import dayjs from "dayjs";
import Duration from "dayjs/plugin/duration";
import LocalizedFormat from "dayjs/plugin/localizedFormat";
import RelativeTime from "dayjs/plugin/relativeTime";
import Timezone from "dayjs/plugin/timezone";
import utc from "dayjs/plugin/utc";
import "locales/i18n";
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClientProvider } from "@tanstack/react-query";
import { queryClient } from "utils/queryClient";
import App from "./App";
import "index.scss";
import "rapido-ui/tailwind.css";
import { initAppearance } from "rapido-ui/appearance";
import { cachedPanelAppearance } from "rapido-ui/panelTheme";

dayjs.extend(Timezone);
dayjs.extend(LocalizedFormat);
dayjs.extend(utc);
dayjs.extend(RelativeTime);
dayjs.extend(Duration);

// The look (theme preset, depth, motion, ...) is the user's Appearance
// choice from localStorage, applied to <html> before the first render so
// there is no flash of the wrong theme. It also sets the browser chrome colour.
initAppearance(cachedPanelAppearance());

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    {/* One root QueryClient for the whole app (see utils/queryClient.ts) -
        the old dashboard had two: this app-wide one and OverviewNew.tsx's
        own private instance, which meant a write on the Users page could
        never invalidate Overview's cached stats. */}
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </React.StrictMode>
);
