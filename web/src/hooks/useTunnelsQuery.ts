import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { Tunnel, TunnelCreatePayload, TunnelDeleteResult } from "types/Tunnel";
import { queryKeys } from "utils/queryClient";

const IN_FLIGHT_STATUSES = new Set(["pending", "deleting", "stopping", "starting"]);

export const useTunnelsQuery = () =>
  useQuery({
    queryKey: queryKeys.tunnels,
    queryFn: () => fetch<Tunnel[]>("/tunnels"),
    // Provisioning/teardown/stop/start/restart all run in the background on
    // the server (see handleCreateTunnel's own doc comment) - poll quickly
    // only while a row is actually mid-flight, same shape as
    // useNodesQuery's "connecting" poll, so a fleet with nothing in flight
    // makes no background requests at all.
    refetchInterval: (query) =>
      (query.state.data ?? []).some((t) => IN_FLIGHT_STATUSES.has(t.status)) ? 3000 : 30_000,
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

const useTunnelActionMutation = (action: "stop" | "start" | "restart") => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: number) => fetch<Tunnel>(`/tunnels/${id}/${action}`, { method: "POST" }),
    onSuccess: () => invalidateTunnels(queryClient),
  });
};

export const useStopTunnelMutation = () => useTunnelActionMutation("stop");
export const useStartTunnelMutation = () => useTunnelActionMutation("start");
export const useRestartTunnelMutation = () => useTunnelActionMutation("restart");
