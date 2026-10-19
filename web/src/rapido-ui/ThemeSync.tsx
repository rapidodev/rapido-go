import { FC, useEffect, useState } from "react";
import { useThemeQuery } from "hooks/useThemeQuery";
import { getAuthToken } from "utils/authStorage";
import { applyServerTheme } from "./panelTheme";

/**
 * Keeps the panel default theme in step with the server. Mounted once at the app root: once a
 * visitor is signed in it asks for GET /settings/theme and applies the answer - but only to a visitor
 * with no look of their own (that rule lives in setPanelDefault). The last answer is cached, so the
 * first paint of the next visit already has it; a failed request leaves whatever is in force.
 */
export const ThemeSync: FC = () => {
  const [signedIn, setSignedIn] = useState(() => !!getAuthToken());
  useEffect(() => {
    const check = () => setSignedIn(!!getAuthToken());
    window.addEventListener("hashchange", check);
    window.addEventListener("storage", check);
    return () => {
      window.removeEventListener("hashchange", check);
      window.removeEventListener("storage", check);
    };
  }, []);
  const { data } = useThemeQuery(signedIn);
  useEffect(() => {
    if (data) applyServerTheme(data.theme);
  }, [data]);
  return null;
};
