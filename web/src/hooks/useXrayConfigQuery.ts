import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { XrayConfig, XrayConfigWritePayload } from "types/XrayConfig";
import { queryKeys } from "utils/queryClient";

export const useXrayConfigQuery = () =>
  useQuery({
    queryKey: queryKeys.xrayConfig,
    queryFn: () => fetch<XrayConfig>("/settings/xray-config"),
  });

// PUT /api/settings/xray-config is a full-object replace of both halves
// together (see internal/httpapi/xrayconfig.go's own doc comment on why
// inbounds are synced before core config server-side) - the caller always
// sends the whole document back, same convention as the old separate
// PUT /hosts and PUT /settings/core-config. On success, also invalidates
// the plain inbounds tag list and the hosts map: a newly-added inbound tag
// needs to show up immediately in InboundsPicker.tsx and in this same
// page's embedded HostsAdmin "add host" tag picker, and a renamed/removed-
// via-this-page inbound's hosts need the same live refresh.
export type SaveXrayConfig = {
  body: XrayConfigWritePayload;
  // Removing a protocol's last inbound deletes those users' credentials for it,
  // so the server refuses that until it has been asked for in so many words.
  confirmDropCredentials?: boolean;
};

export type DroppedCredentials = { protocol: string; users: number };

/** What the server says would be lost, when it refused a save for that reason. */
export const droppedCredentials = (error: unknown): DroppedCredentials[] | null => {
  const data = (
    error as { response?: { _data?: { confirm_required?: string; dropped_credentials?: DroppedCredentials[] } } }
  )?.response?._data;
  return data?.confirm_required === "drop_credentials" ? data.dropped_credentials ?? [] : null;
};

export const useSaveXrayConfigMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ body, confirmDropCredentials }: SaveXrayConfig) =>
      fetch<XrayConfig>("/settings/xray-config", {
        method: "PUT",
        body,
        query: confirmDropCredentials ? { confirm_drop_credentials: "true" } : undefined,
      }),
    onSuccess: (data) => {
      queryClient.setQueryData(queryKeys.xrayConfig, data);
      queryClient.invalidateQueries({ queryKey: queryKeys.inbounds });
      queryClient.invalidateQueries({ queryKey: queryKeys.hosts });
      queryClient.invalidateQueries({ queryKey: queryKeys.coreSnapshots });
    },
  });
};
