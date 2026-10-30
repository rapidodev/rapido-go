import { Suspense, lazy } from "react";
import { Navigate, createHashRouter, redirect } from "react-router-dom";
import { loadCurrentAdminForNavigation } from "hooks/useCurrentAdminQuery";
import { SudoOnly } from "rapido-ui/SudoOnly";
import { extensionRoutes } from "extensions";
import { getAuthToken } from "utils/authStorage";
import { setLoginRedirect } from "utils/session";
import { Login } from "./Login";
import { pageImports } from "./pageImports";
import { RouteError } from "./RouteError";

// With no token there is nothing to look up, and an outage must not hide the
// login form behind an error page. A failed /admin fetch throws: a 401 has
// already sent the admin to the login page (service/http.ts), and anything else
// lands on `errorElement: <RouteError/>` below with the session untouched.
//
// Seeds the query cache with the result too (a small improvement over the
// old dashboard's loader, which just returned the fetch and let
// Shell/SudoOnly's own useGetUser hit the network again) - useCurrentAdminQuery
// reads the exact same queryKey, so Shell/SudoOnly render with data already
// in hand instead of a second round trip on every navigation.
const fetchAdminLoader = async () => {
  if (!getAuthToken()) return redirect("/login");
  return loadCurrentAdminForNavigation();
};

// Lazy-loaded so each page's dependencies are a separate chunk that Login
// never has to fetch or evaluate - a page-specific bug can't take down
// unrelated routes.
const RapidoHome = lazy(pageImports.overview);
const UsersPage = lazy(pageImports.users);
const TicketsPage = lazy(pageImports.tickets);
const HostsPage = lazy(pageImports.hosts);
const CoreConfigPage = lazy(pageImports.coreConfig);
const AdminsPage = lazy(pageImports.admins);
const IntegrationsPage = lazy(pageImports.integrations);
const UserTemplatesPage = lazy(pageImports.templates);
const NodesPage = lazy(pageImports.nodes);
const MonitoringPage = lazy(pageImports.monitoring);
const TunnelsPage = lazy(pageImports.tunnels);
const LogsPage = lazy(pageImports.logs);
const BackupsPage = lazy(pageImports.backups);
const GatewayPage = lazy(pageImports.gateway);
const AppearancePage = lazy(pageImports.appearance);
const ThemePage = lazy(pageImports.theme);

export const router = createHashRouter([
  {
    path: "/",
    element: (
      <Suspense fallback={null}>
        <RapidoHome />
      </Suspense>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/users/",
    element: (
      <Suspense fallback={null}>
        <UsersPage />
      </Suspense>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    // Not sudo-gated: GET /api/user_template is requireAdmin, not
    // requireSudo - every admin can browse/use these presets, only the
    // sudo-only write endpoints (create/edit/delete) are restricted, which
    // UserTemplatesPage itself enforces by hiding those actions.
    path: "/templates/",
    element: (
      <Suspense fallback={null}>
        <UserTemplatesPage />
      </Suspense>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    // Not sudo-gated: GET /api/tickets is requireAdmin, not requireSudo -
    // every admin can read/answer their own customers' tickets, scoped
    // server-side (internal/httpapi/tickets.go's scopedAdminID); only the
    // `owner` column is sudo-conditional, and TicketsAdmin itself hides it.
    path: "/tickets/",
    element: (
      <Suspense fallback={null}>
        <TicketsPage />
      </Suspense>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/hosts/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <HostsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/core-config/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <CoreConfigPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/admins/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <AdminsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/integrations/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <IntegrationsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/backups/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <BackupsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    // Every admin can restyle their own panel; nothing here is server-side.
    path: "/appearance/",
    element: (
      <Suspense fallback={null}>
        <AppearancePage />
      </Suspense>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    // Designing the panel-wide theme changes every admin's default look: sudo only. Other
    // admins keep the personal Appearance dialog.
    path: "/theme/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <ThemePage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/gateway/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <GatewayPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/nodes/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <NodesPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/monitoring/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <MonitoringPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/tunnels/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <TunnelsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  {
    path: "/logs/",
    element: (
      <SudoOnly>
        <Suspense fallback={null}>
          <LogsPage />
        </Suspense>
      </SudoOnly>
    ),
    errorElement: <RouteError />,
    loader: fetchAdminLoader,
  },
  // Pages an optional add-on contributes (none in the public build).
  ...extensionRoutes().map(({ path, sudoOnly, component: Page }) => {
    const page = (
      <Suspense fallback={null}>
        <Page />
      </Suspense>
    );
    return {
      path,
      element: sudoOnly ? <SudoOnly>{page}</SudoOnly> : page,
      errorElement: <RouteError />,
      loader: fetchAdminLoader,
    };
  }),
  {
    path: "/login/",
    element: <Login />,
  },
  {
    // Catches any other hash path - old bookmarks to retired routes
    // (Settings, from the previous dashboard, or a typo'd URL) land here
    // instead of react-router's default error UI. Nodes/Monitoring used to
    // be in that boat too; both now have real Go-backed pages above.
    path: "*",
    element: <Navigate to="/" replace />,
  },
]);

// Already on the login page there is nowhere to go, and a second navigation
// would wipe the "session expired" note the first one left there.
setLoginRedirect((expired) => {
  if (router.state.location.pathname.startsWith("/login")) return;
  void router.navigate("/login", { replace: true, state: { sessionExpired: expired } });
});
