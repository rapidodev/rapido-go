import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import {
  Tunnel,
  TunnelAdoptPayload,
  TunnelCreatePayload,
  TunnelDeleteResult,
  TunnelDiscoverPayload,
  TunnelDiscoverResult,
  TunnelSamples,
  TunnelUpdatePayload,
} from "types/Tunnel";
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
      (query.state.data ?? []).some((t) => IN_FLIGHT_STATUSES.has(t.status)) ? 3000 : 15_000,
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

// Read-only: looks at the servers and reports, so it changes no cached data.
export const useDiscoverTunnelMutation = () =>
  useMutation({
    mutationFn: (body: TunnelDiscoverPayload) =>
      fetch<TunnelDiscoverResult>("/tunnels/discover", { method: "POST", body }),
  });

export const useAdoptTunnelMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: TunnelAdoptPayload) => fetch<Tunnel>("/tunnels/adopt", { method: "POST", body }),
    onSuccess: () => invalidateTunnels(queryClient),
  });
};

export const useUpdateTunnelMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, body }: { id: number; body: TunnelUpdatePayload }) =>
      fetch<Tunnel>(`/tunnels/${id}`, { method: "PATCH", body }),
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

// History of each tunnel (sudo). The endpoint is newer than the rest of the tunnel API, so a
// panel without it just answers an error: callers treat that as "no history" and draw nothing
// for that tunnel - they never invent numbers, and a failure is not retried.
export const useTunnelSamplesQueries = (ids: number[], hours = 6, enabled = true) =>
  useQueries({
    queries: ids.map((id) => ({
      queryKey: [...queryKeys.tunnels, "samples", id, hours] as const,
      queryFn: () => fetch<TunnelSamples>(`/tunnels/${id}/samples?hours=${hours}`),
      enabled,
      retry: false,
      refetchInterval: 60_000,
    })),
  });
