import { FC, useEffect, useSyncExternalStore } from "react";
import { useThemeQuery } from "hooks/useThemeQuery";
import { getAuthToken, subscribeAuthToken } from "utils/authStorage";
import { applyServerTheme } from "./panelTheme";

/**
 * Keeps the panel default theme in step with the server. Mounted once at the app root: once a
 * visitor is signed in it asks for GET /settings/theme and applies the answer - but only to a visitor
 * with no look of their own (that rule lives in setPanelDefault). The last answer is cached, so the
 * first paint of the next visit already has it; a failed request leaves whatever is in force.
 */
export const ThemeSync: FC = () => {
  // Signing in changes the page through the router's pushState, which fires no hashchange, so
  // this listens to the token itself.
  const signedIn = useSyncExternalStore(subscribeAuthToken, () => !!getAuthToken());
  const { data } = useThemeQuery(signedIn);
  useEffect(() => {
    if (data) applyServerTheme(data.theme);
  }, [data]);
  return null;
};
