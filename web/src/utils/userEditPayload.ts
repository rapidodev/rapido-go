import {
  DataLimitResetStrategy,
  ProxySettingsWriteMap,
  Status,
  User,
  UserInbounds,
  UserWritePayload,
} from "types/User";
import {
  DAY_SECONDS,
  dataLimitToGb,
  daysToExpire,
  expireToDays,
  gbToDataLimit,
} from "utils/userConversions";

export type UserEditValues = {
  status: string;
  data_limit_gb: string;
  data_limit_reset_strategy: string;
  expire_days: string;
  on_hold_days: string;
  note: string;
};

/** What the edit form starts from. A save only sends what moved away from it. */
export const userEditValues = (user: User, now: number = Date.now()): UserEditValues => ({
  status: user.status,
  data_limit_gb: dataLimitToGb(user.data_limit)?.toString() ?? "",
  data_limit_reset_strategy: user.data_limit_reset_strategy,
  expire_days: expireToDays(user.expire, now)?.toString() ?? "",
  on_hold_days: user.on_hold_expire_duration
    ? (user.on_hold_expire_duration / DAY_SECONDS).toString()
    : "",
  note: user.note ?? "",
});

/**
 * The statuses a user can be saved with: "limited" and "expired" are only ever
 * derived from usage and dates, so the API refuses them as a target. The current
 * one stays on the list, or the select would show something else than the truth.
 */
export const userStatusOptions = (current: Status): Status[] =>
  Array.from(new Set<Status>([current, "active", "on_hold", "disabled"]));

export const onHoldDaysMissing = (values: UserEditValues): boolean =>
  values.status === "on_hold" && !(Number(values.on_hold_days) > 0);

const sameTags = (a: string[], b: Set<string>): boolean =>
  a.length === b.size && a.every((tag) => b.has(tag));

/**
 * The PUT body for an edit: only what the admin changed.
 *
 * The API is a partial update, and each field sent has a price. A protocol's
 * empty settings used to mean a new credential, which cut off the user's clients;
 * a recomputed expiry moved by up to a day on every save; and a status of
 * "limited" or an on-hold user without its duration was refused outright, so
 * those users could not be saved at all. Clearing goes out as 0: null means
 * "unchanged" to the API.
 */
export const buildUserEdit = (
  user: User,
  values: UserEditValues,
  selectedProtocols: Set<string>,
  selectedInbounds: Record<string, Set<string>>,
  touchedInboundProtocols: Set<string>,
  now: number = Date.now()
): UserWritePayload => {
  const initial = userEditValues(user, now);
  const edit: UserWritePayload = {};

  if (values.status !== initial.status) edit.status = values.status as Status;

  if (values.data_limit_gb !== initial.data_limit_gb) {
    edit.data_limit = gbToDataLimit(Number(values.data_limit_gb)) ?? 0;
  }
  // No point scheduling a reset for a limit that doesn't exist.
  const limitGone = values.data_limit_gb === "" || Number(values.data_limit_gb) === 0;
  const strategy = limitGone ? "no_reset" : values.data_limit_reset_strategy;
  if (strategy !== initial.data_limit_reset_strategy) {
    edit.data_limit_reset_strategy = strategy as DataLimitResetStrategy;
  }

  if (values.status === "on_hold") {
    // The API refuses an on-hold user with an expiry: it starts on first use.
    if (user.expire) edit.expire = 0;
    if (values.status !== initial.status || values.on_hold_days !== initial.on_hold_days) {
      edit.on_hold_expire_duration = Math.round(Number(values.on_hold_days) * DAY_SECONDS);
    }
  } else if (values.expire_days !== initial.expire_days) {
    edit.expire = daysToExpire(Number(values.expire_days), now) ?? 0;
  }

  if (values.note !== initial.note) edit.note = values.note;

  const proxies: ProxySettingsWriteMap = {};
  selectedProtocols.forEach((protocol) => {
    if (!user.proxies[protocol]) proxies[protocol] = {};
  });
  Object.keys(user.proxies).forEach((protocol) => {
    if (!selectedProtocols.has(protocol)) proxies[protocol] = null;
  });
  if (Object.keys(proxies).length > 0) edit.proxies = proxies;

  const inbounds: UserInbounds = {};
  selectedProtocols.forEach((protocol) => {
    const chosen = selectedInbounds[protocol];
    if (!chosen || !touchedInboundProtocols.has(protocol)) return;
    if (!sameTags(user.inbounds?.[protocol] ?? [], chosen)) {
      inbounds[protocol] = Array.from(chosen);
    }
  });
  if (Object.keys(inbounds).length > 0) edit.inbounds = inbounds;

  return edit;
};
