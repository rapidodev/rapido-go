import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { HostsMap } from "types/Host";
import { queryKeys } from "utils/queryClient";

export const useHostsQuery = () =>
  useQuery({
    queryKey: queryKeys.hosts,
    // A stalled browser connection must not leave the page loading forever.
    // Cancel the request when the user leaves this page.
    queryFn: async ({ signal }) => {
      const controller = new AbortController();
      const cancel = () => controller.abort(signal.reason);
      if (signal.aborted) cancel();
      else signal.addEventListener("abort", cancel, { once: true });
      const timer = setTimeout(() => controller.abort(new DOMException("Request timed out", "TimeoutError")), 8000);
      try {
        return await fetch<HostsMap>("/hosts", { signal: controller.signal, retry: 0 });
      } finally {
        clearTimeout(timer);
        signal.removeEventListener("abort", cancel);
      }
    },
    retry: 1,
    retryDelay: 500,
  });

// PUT /api/hosts is a full replace, not a per-tag patch (see
// internal/httpapi/hosts.go's handlePutHosts) - the caller must always send
// the entire map back, every tag, not just the one it edited. That invariant
// is HostsAdmin.tsx's responsibility to uphold (it keeps one full in-memory
// copy and only ever PUTs the whole thing); this hook just performs the
// write and refreshes the cache with whatever the backend echoes back.
export const useSaveHostsMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: HostsMap) =>
      fetch<HostsMap>("/hosts", { method: "PUT", body }),
    onSuccess: (data) => {
      queryClient.setQueryData(queryKeys.hosts, data);
    },
  });
};
