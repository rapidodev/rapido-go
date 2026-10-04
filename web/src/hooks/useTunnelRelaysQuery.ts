import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { TunnelRelay, TunnelRelayWritePayload } from "types/TunnelRelay";
import { queryKeys } from "utils/queryClient";

export const useTunnelRelaysQuery = () =>
  useQuery({
    queryKey: queryKeys.tunnelRelays,
    queryFn: () => fetch<TunnelRelay[]>("/tunnel-relays"),
    // Matches the backend probe's own cadence (internal/relayhealth, 30s) -
    // polling faster would only ever show the same published snapshot again.
    refetchInterval: 30_000,
  });

const invalidateTunnelRelays = (queryClient: ReturnType<typeof useQueryClient>) =>
  queryClient.invalidateQueries({ queryKey: queryKeys.tunnelRelays });

export const useCreateTunnelRelayMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: TunnelRelayWritePayload) =>
      fetch<TunnelRelay>("/tunnel-relays", { method: "POST", body }),
    onSuccess: () => invalidateTunnelRelays(queryClient),
  });
};

export const useDeleteTunnelRelayMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => fetch(`/tunnel-relays/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateTunnelRelays(queryClient),
  });
};
