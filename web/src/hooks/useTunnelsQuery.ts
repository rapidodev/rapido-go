import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { Tunnel, TunnelCreatePayload, TunnelDeleteResult } from "types/Tunnel";
import { queryKeys } from "utils/queryClient";

export const useTunnelsQuery = () =>
  useQuery({
    queryKey: queryKeys.tunnels,
    queryFn: () => fetch<Tunnel[]>("/tunnels"),
    // Provisioning/teardown runs in the background on the server (see
    // handleCreateTunnel's own doc comment) - poll quickly only while a row
    // is actually mid-flight ("pending"/"deleting"), same shape as
    // useNodesQuery's "connecting" poll, so a fleet with nothing in flight
    // makes no background requests at all.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((t) => t.status === "pending" || t.status === "deleting") ? 3000 : false,
  });

const invalidateTunnels = (queryClient: ReturnType<typeof useQueryClient>) =>
  queryClient.invalidateQueries({ queryKey: queryKeys.tunnels });

export const useCreateTunnelMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: TunnelCreatePayload) => fetch<Tunnel>("/tunnels", { method: "POST", body }),
    onSuccess: () => invalidateTunnels(queryClient),
  });
};

export const useDeleteTunnelMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => fetch<TunnelDeleteResult>(`/tunnels/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateTunnels(queryClient),
  });
};
