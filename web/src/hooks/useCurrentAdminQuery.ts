import { useQuery } from "@tanstack/react-query";
import { isUnauthorized } from "service/errors";
import { fetch } from "service/http";
import { Admin } from "types/Admin";
import { queryClient, queryKeys } from "utils/queryClient";
import { getAuthToken } from "utils/authStorage";
import { clearSessionData } from "utils/session";

// Replaces the old dashboard's hooks/useGetUser.tsx (which used the retired
// react-query v3 useQuery with no queryKey at all - every caller shared one
// implicit cache slot only by coincidence). GET /api/admin identifies the
// logged-in admin: Shell reads `is_sudo` to filter nav items, SudoOnly reads
// it to gate whole pages, and the Users/Admins pages use it to tell "this
// row is me" apart from everyone else.
let identitySession: string | null = null;
export const fetchCurrentAdmin = async () => {
  const session = getAuthToken();
  identitySession = session;
  const admin = await fetch<Admin>("/admin");
  if (session !== getAuthToken()) throw new Error("Session changed during identity lookup");
  return admin;
};

const currentAdminOptions = () => ({
  queryKey: queryKeys.currentAdmin,
  queryFn: fetchCurrentAdmin,
  staleTime: 15_000,
  retry: false as const,
});

// Route changes share the same short-lived identity cache as mounted hooks.
// Backend authorization remains authoritative for every protected request.
const resetChangedSession = () => {
  const session = getAuthToken();
  if (!session || session !== identitySession) clearSessionData();
};

// The login page emptied the cache when it opened, so the token it stores next
// is not a change to reset for: that would only cancel what the new session has
// already started (ThemeSync's request, for one).
export const adoptNewSession = () => {
  identitySession = getAuthToken();
};

export const loadCurrentAdmin = () => {
  resetChangedSession();
  return queryClient.fetchQuery(currentAdminOptions());
};

// A slow identity refresh must not hold every route behind the network.
// Reuse a lookup from this session, unless the panel has since refused it;
// refresh in the background.
export const loadCurrentAdminForNavigation = () => {
  resetChangedSession();
  const state = queryClient.getQueryState<Admin>(queryKeys.currentAdmin);
  if (state?.data && !isUnauthorized(state.error)) {
    void queryClient.prefetchQuery(currentAdminOptions());
    return Promise.resolve(state.data);
  }
  return queryClient.fetchQuery(currentAdminOptions());
};

export const useCurrentAdminQuery = () =>
  useQuery(currentAdminOptions());

// Who this session last proved to be. A refresh that failed for any reason but
// a 401 (a dropped connection, a panel restarting) says nothing new about the
// role, so an admin is not bounced off the page they are working on.
const provenAdmin = ({ data, error }: { data?: Admin; error: unknown }) =>
  isUnauthorized(error) ? undefined : data;

// Fails closed: false until /admin has resolved (and for a lookup that never
// succeeded), so a query the backend answers with 403 for a reseller is never
// sent while the role is still unknown.
export const useIsSudo = () => !!provenAdmin(useCurrentAdminQuery())?.is_sudo;

// Same, for what only an owner may do.
export const useIsOwner = () => !!provenAdmin(useCurrentAdminQuery())?.is_owner;
