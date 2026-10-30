import { removeAuthToken } from "utils/authStorage";
import { queryClient } from "utils/queryClient";

// Everything a signed-in admin leaves behind in this tab: the token, the query
// cache, and the UI state that outlives a page (the Users filters and open
// dialogs, the rolling series). That state registers its own reset here, so
// its code stays in the chunk of the page that uses it and one admin's view
// never shows for the next one on the same browser.
const resets = new Set<() => void>();

export const onSessionClear = (reset: () => void): void => {
  resets.add(reset);
};

/** What the previous session left in memory, when the token itself has already moved on. */
export const clearSessionData = (): void => {
  queryClient.clear();
  resets.forEach((reset) => reset());
};

export const clearSession = (): void => {
  removeAuthToken();
  clearSessionData();
};

// The way to the login page is handed in by pages/Router.tsx rather than
// imported: the router pulls in every page, and every page pulls in
// service/http.ts, which is what calls endSession.
let goToLogin: (expired: boolean) => void = () => undefined;

export const setLoginRedirect = (redirect: (expired: boolean) => void): void => {
  goToLogin = redirect;
};

/** The panel refused this session. `expired` is false when there never was a
 * token, so a first visit is not told its session expired. */
export const endSession = (expired: boolean): void => {
  clearSession();
  goToLogin(expired);
};
