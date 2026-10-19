import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { fetch } from "service/http";
import { PanelTheme, PanelThemeResponse } from "rapido-ui/panelTheme";

// The panel-wide default theme (GET is open to every signed-in admin, PUT and DELETE are
// sudo). It is asked for once per session, quietly: a panel without it, or a failed
// request, simply leaves the built-in look in place.

const KEY = ["panelTheme"] as const;

export const useThemeQuery = (enabled = true) =>
  useQuery({
    queryKey: KEY,
    queryFn: () => fetch<PanelThemeResponse>("/settings/theme"),
    enabled,
    retry: false,
    staleTime: 5 * 60_000,
    refetchOnWindowFocus: false,
  });

export const useSaveThemeMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (theme: PanelTheme) => fetch<PanelThemeResponse>("/settings/theme", { method: "PUT", body: { theme } }),
    onSuccess: (data) => queryClient.setQueryData(KEY, data),
  });
};

export const useResetThemeMutation = () => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => fetch<PanelThemeResponse>("/settings/theme", { method: "DELETE" }),
    onSuccess: (data) => queryClient.setQueryData(KEY, data),
  });
};
