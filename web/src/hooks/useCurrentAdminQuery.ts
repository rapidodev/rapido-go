import { useQuery } from "@tanstack/react-query";
import { fetch } from "service/http";
import { Admin } from "types/Admin";
import { queryClient, queryKeys } from "utils/queryClient";
import { getAuthToken } from "utils/authStorage";

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
  if (!session || session !== identitySession) queryClient.clear();
};

export const loadCurrentAdmin = () => {
  resetChangedSession();
  return queryClient.fetchQuery(currentAdminOptions());
};

// A slow identity refresh must not hold every route behind the network.
// Reuse only a successful lookup from this session; refresh in the background.
export const loadCurrentAdminForNavigation = () => {
  resetChangedSession();
  const state = queryClient.getQueryState<Admin>(queryKeys.currentAdmin);
  if (state?.status === "success" && state.data) {
    void queryClient.prefetchQuery(currentAdminOptions());
    return Promise.resolve(state.data);
  }
  return queryClient.fetchQuery(currentAdminOptions());
};

export const useCurrentAdminQuery = () =>
  useQuery(currentAdminOptions());

// Fails closed: false until /admin has resolved (and for a failed lookup), so
// a query the backend answers with 403 for a reseller is never sent while the
// role is still unknown.
export const useIsSudo = () => {
  const { data, isSuccess } = useCurrentAdminQuery();
  return isSuccess && !!data?.is_sudo;
};
