import { FetchOptions, ResponseType, $fetch as ohMyFetch } from "ofetch";
import { getAuthToken } from "utils/authStorage";
import { endSession } from "utils/session";

// The one request whose 401 says nothing about the session: it is how the
// login form learns the password was wrong.
const LOGIN_PATH = "/admin/token";

export const $fetch = ohMyFetch.create({
  baseURL: import.meta.env.VITE_BASE_API,
  // A 401 means the token is no longer any good, and every other request will
  // get the same answer, so the session ends here instead of leaving the page
  // full of errors. Only for the token in force: the answer to a request from
  // before a new login says nothing about the session that followed it, and
  // once the first 401 has cleared the token the rest of a burst do not match.
  onResponseError({ request, options, response }) {
    if (response.status !== 401 || String(request).endsWith(LOGIN_PATH)) return;
    const token = getAuthToken();
    if (options.headers.get("Authorization") === (token ? `Bearer ${token}` : null)) endSession(!!token);
  },
});

// Generic over ResponseType (defaulting to "json", same as every existing
// call site) rather than hardcoded to it - hooks/useBackupsQuery.ts's
// downloadBackup is the first caller that needs responseType: "blob" to
// pull down a real file instead of a parsed JSON body.
export const fetcher = <T = any, R extends ResponseType = "json">(
  url: string,
  ops: FetchOptions<R> = {} as FetchOptions<R>
) => {
  const token = getAuthToken();
  if (token) {
    ops["headers"] = {
      ...(ops?.headers || {}),
      Authorization: `Bearer ${getAuthToken()}`,
    };
  }
  return $fetch<T, R>(url, ops);
};

export const fetch = fetcher;
