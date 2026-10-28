import { useQuery } from "@tanstack/react-query";
import { fetch } from "service/http";
import { Admin } from "types/Admin";
import { queryClient, queryKeys } from "utils/queryClient";

// Replaces the old dashboard's hooks/useGetUser.tsx (which used the retired
// react-query v3 useQuery with no queryKey at all - every caller shared one
// implicit cache slot only by coincidence). GET /api/admin identifies the
// logged-in admin: Shell reads `is_sudo` to filter nav items, SudoOnly reads
// it to gate whole pages, and the Users/Admins pages use it to tell "this
// row is me" apart from everyone else.
export const fetchCurrentAdmin = () => fetch<Admin>("/admin");

const currentAdminOptions = () => ({
  queryKey: queryKeys.currentAdmin,
  queryFn: fetchCurrentAdmin,
  staleTime: 15_000,
  retry: false as const,
});

// Route changes share the same short-lived identity cache as mounted hooks.
// Backend authorization remains authoritative for every protected request.
export const loadCurrentAdmin = () => queryClient.fetchQuery(currentAdminOptions());

export const useCurrentAdminQuery = () =>
  useQuery(currentAdminOptions());

// Fails closed: false until /admin has resolved (and for a failed lookup), so
// a query the backend answers with 403 for a reseller is never sent while the
// role is still unknown.
export const useIsSudo = () => {
  const { data, isSuccess } = useCurrentAdminQuery();
  return isSuccess && !!data?.is_sudo;
};
