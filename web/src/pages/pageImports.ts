export const pageImports = {
  overview: () => import("./RapidoHome"),
  users: () => import("./UsersPage"),
  tickets: () => import("./TicketsPage"),
  hosts: () => import("./HostsPage"),
  coreConfig: () => import("./CoreConfigPage"),
  admins: () => import("./AdminsPage"),
  integrations: () => import("./IntegrationsPage"),
  templates: () => import("./UserTemplatesPage"),
  nodes: () => import("./NodesPage"),
  monitoring: () => import("./MonitoringPage"),
  tunnels: () => import("./TunnelsPage"),
  logs: () => import("./LogsPage"),
  backups: () => import("./BackupsPage"),
  gateway: () => import("./GatewayPage"),
  appearance: () => import("./AppearancePage"),
  theme: () => import("./ThemePage"),
};

type PageLoaders = Record<string, () => Promise<unknown>>;

export const createPagePrefetcher = (loaders: PageLoaders) => {
  const pending = new Map<string, Promise<unknown>>();
  return async (path: string): Promise<void> => {
    const connection = typeof navigator === "undefined" ? undefined :
      (navigator as Navigator & { connection?: { saveData?: boolean; effectiveType?: string } }).connection;
    if (connection?.saveData || connection?.effectiveType === "2g" || connection?.effectiveType === "slow-2g") return;
    const load = loaders[path];
    if (!load) return;
    let request = pending.get(path);
    if (!request) {
      request = load();
      pending.set(path, request);
    }
    try {
      await request;
    } catch {
      // A speculative download failure must not block a later navigation.
      pending.delete(path);
    }
  };
};

export const prefetchPage = createPagePrefetcher({
  "/": pageImports.overview,
  "/users/": pageImports.users,
  "/tickets/": pageImports.tickets,
  "/hosts/": pageImports.hosts,
  "/core-config/": pageImports.coreConfig,
  "/admins/": pageImports.admins,
  "/integrations/": pageImports.integrations,
  "/templates/": pageImports.templates,
  "/nodes/": pageImports.nodes,
  "/monitoring/": pageImports.monitoring,
  "/tunnels/": pageImports.tunnels,
  "/logs/": pageImports.logs,
  "/backups/": pageImports.backups,
  "/gateway/": pageImports.gateway,
  "/appearance/": pageImports.appearance,
  "/theme/": pageImports.theme,
});
