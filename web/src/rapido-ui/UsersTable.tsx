import { FC, ReactNode, useEffect, useMemo, useState } from "react";
import { ArrowPathIcon, EllipsisHorizontalIcon, KeyIcon, LinkIcon, PencilSquareIcon, TrashIcon } from "@heroicons/react/24/outline";
import { useTranslation } from "react-i18next";
import debounce from "lodash.debounce";
import classNames from "classnames";
import { useUsersQuery, UsersFilters, downloadUsersCsv } from "hooks/useUsersQuery";
import { useAdminsQuery } from "hooks/useAdminsQuery";
import { useIsSudo } from "hooks/useCurrentAdminQuery";
import { useUsersUiStore } from "rapido-ui/usersUiStore";
import { User, Status } from "types/User";
import { formatBytes } from "utils/formatByte";
import { relativeExpiryDate } from "utils/dateFormatter";
import { LastSeen, lastSeenOf } from "utils/presence";
import { setUsersPerPageLimitSize } from "utils/userPreferenceStorage";
import { Card, CardTitle, CardSubtitle } from "rapido-ui/Card";
import { Badge } from "rapido-ui/Badge";
import { LOAD_ZONES, solidZones } from "rapido-ui/Gauge";
import { GAUGE_BAD, GAUGE_CYAN, GAUGE_GOOD, GAUGE_VIOLET, GAUGE_WARN, TYPE } from "rapido-ui/instrument";
import { Cluster, ClusterZone } from "rapido-ui/Cluster";
import { SegmentBar } from "rapido-ui/Segments";
import { StatusPill } from "rapido-ui/StatusPill";
import { useSystemStatsQuery } from "hooks/useSystemStatsQuery";
import { ResourceMeter } from "rapido-ui/ResourceMeter";
import { useAppearance } from "rapido-ui/appearance";
import { Button } from "rapido-ui/Button";
import { Input } from "rapido-ui/Input";
import { Select } from "rapido-ui/Select";
import { ltrIsolate } from "rapido-ui/bidi";

import type { BadgeTone } from "rapido-ui/Badge";

// limited (out of data) and expired both read as red: from the admin's point
// of view they are the same situation - the customer cannot connect and needs
// to renew - so they should stand out identically when scanning the list.
const statusTone: Record<Status, BadgeTone> = {
  active: "green",
  on_hold: "yellow",
  limited: "red",
  expired: "red",
  disabled: "gray",
};

const statusFilterOptions: { value: Status | ""; labelKey: string }[] = [
  { value: "", labelKey: "rapido.allStatuses" },
  { value: "active", labelKey: "status.active" },
  { value: "on_hold", labelKey: "status.on_hold" },
  { value: "limited", labelKey: "status.limited" },
  { value: "expired", labelKey: "status.expired" },
  { value: "disabled", labelKey: "status.disabled" },
];

const sortOptions: { value: string; labelKey: string }[] = [
  { value: "-created_at", labelKey: "rapido.sortNewest" },
  { value: "created_at", labelKey: "rapido.sortOldest" },
  { value: "username", labelKey: "rapido.sortUsernameAz" },
  { value: "-used_traffic", labelKey: "rapido.sortMostUsed" },
  { value: "expire", labelKey: "rapido.sortSoonestExpiry" },
];

// Card edge + faint tint per state. Written as whole class strings because
// Tailwind only generates classes it can find literally in the source.
//
// The `!` matters: Card already sets `border-rapido-border`, and when two
// utilities set the same property the winner is decided by their order in the
// generated stylesheet, not by the order they appear in the class attribute.
// Without it the grey default won and every card looked identical - which is
// exactly what the colouring was supposed to fix.
type CardTone = BadgeTone;

const cardToneClasses: Record<CardTone, string> = {
  green: "!border-emerald-500/60",
  sky: "!border-sky-500/60",
  yellow: "!border-yellow-500/60",
  orange: "!border-orange-500/60",
  red: "!border-red-500/60",
  brand: "!border-rapido-accent/60 bg-rapido-accent/[0.04]",
  violet: "!border-rapido-violet/60 bg-rapido-violet/[0.04]",
  gray: "",
};

// Green pulsing dot while the customer is connected, steady blue once they
// are only "last seen", grey if they never connected - so presence is
// readable at a glance without reading the text.
const PresenceDot: FC<{ kind: LastSeen["kind"] }> = ({ kind }) => {
  if (kind === "online") {
    return (
      <span className="relative flex h-2 w-2 shrink-0">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400 opacity-75" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-emerald-400" />
      </span>
    );
  }
  return (
    <span
      className={classNames(
        "h-2 w-2 shrink-0 rounded-full",
        kind === "seen" ? "bg-sky-400" : "bg-rapido-muted/50"
      )}
    />
  );
};

const PAGE_SIZES = [10, 25, 50, 100, 200];

// First page, last page, and a window around the current one, with "gap"
// markers standing in for the runs that are collapsed - 7700 users at 10 per
// page would otherwise mean 770 buttons.
const buildPageItems = (
  current: number,
  pageCount: number
): (number | "gap")[] => {
  if (pageCount <= 7) {
    return Array.from({ length: pageCount }, (_, i) => i + 1);
  }
  const pages = new Set<number>([1, pageCount, current]);
  if (current - 1 > 1) pages.add(current - 1);
  if (current + 1 < pageCount) pages.add(current + 1);
  if (current <= 3) [2, 3, 4].forEach((p) => p < pageCount && pages.add(p));
  if (current >= pageCount - 2)
    [pageCount - 1, pageCount - 2, pageCount - 3].forEach(
      (p) => p > 1 && pages.add(p)
    );

  const sorted = Array.from(pages).sort((a, b) => a - b);
  const items: (number | "gap")[] = [];
  sorted.forEach((page, index) => {
    if (index > 0 && page - sorted[index - 1] > 1) items.push("gap");
    items.push(page);
  });
  return items;
};

// The actions of a user card as one toolbar: the two everyday ones (Edit, Subscription link) with
// icon and label, the rare ones (Reset usage, Revoke subscription) behind a "..." menu so a narrow
// card does not wrap into three rows, and Delete on its own at the far end, in red.
const ToolButton: FC<{ icon: ReactNode; label: string; tone: "accent" | "sky" | "red"; onClick: () => void; iconOnly?: boolean }> = ({
  icon,
  label,
  tone,
  onClick,
  iconOnly,
}) => (
  <Button
    variant="chip"
    tone={tone}
    onClick={onClick}
    aria-label={iconOnly ? label : undefined}
    title={iconOnly ? label : undefined}
    className="flex items-center gap-1.5"
  >
    <span className="h-3.5 w-3.5 shrink-0 [&>svg]:h-3.5 [&>svg]:w-3.5" aria-hidden="true">
      {icon}
    </span>
    {!iconOnly && label}
  </Button>
);

const UserActions: FC<{ user: User }> = ({ user }) => {
  const { t } = useTranslation();
  const setEditingUser = useUsersUiStore((s) => s.setEditingUser);
  const setDeletingUser = useUsersUiStore((s) => s.setDeletingUser);
  const setResetUsageUser = useUsersUiStore((s) => s.setResetUsageUser);
  const setRevokeSubscriptionUser = useUsersUiStore((s) => s.setRevokeSubscriptionUser);
  const setSubscriptionLinkUser = useUsersUiStore((s) => s.setSubscriptionLinkUser);
  const [menu, setMenu] = useState(false);

  // A Gateway replica (see types/User.ts's own doc comment) can only
  // change via the panel that actually owns it - the backend rejects a
  // direct edit outright (handleModifyUser), so hiding Edit here isn't
  // just cosmetic, it matches a real enforced rule rather than promising
  // something the click would then fail on.
  const isReplica = !!user.synced_from_panel_name;

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {!isReplica && <ToolButton icon={<PencilSquareIcon />} label={String(t("rapido.edit"))} tone="accent" onClick={() => setEditingUser(user)} />}
      <ToolButton icon={<LinkIcon />} label={String(t("rapido.subscriptionLink"))} tone="sky" onClick={() => setSubscriptionLinkUser(user)} />
      <div className="relative" onBlur={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setMenu(false)}>
        <Button
          variant="chip"
          aria-haspopup="menu"
          aria-expanded={menu}
          aria-label={String(t("rapido.users.moreActions"))}
          onClick={() => setMenu((v) => !v)}
        >
          <EllipsisHorizontalIcon className="h-4 w-4" aria-hidden="true" />
        </Button>
        {menu && (
          <div
            role="menu"
            className="absolute bottom-full z-20 mb-1.5 flex min-w-[11rem] flex-col gap-0.5 rounded-lg border border-rapido-border bg-rapido-surface p-1 shadow-rp-3 start-0"
          >
            <button
              type="button"
              role="menuitem"
              className="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-xs text-rapido-warn hover:bg-rapido-raised"
              onClick={() => {
                setMenu(false);
                setResetUsageUser(user);
              }}
            >
              <ArrowPathIcon className="h-3.5 w-3.5" aria-hidden="true" />
              {t("userDialog.resetUsage")}
            </button>
            <button
              type="button"
              role="menuitem"
              className="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-start text-xs text-rapido-warn hover:bg-rapido-raised"
              onClick={() => {
                setMenu(false);
                setRevokeSubscriptionUser(user);
              }}
            >
              <KeyIcon className="h-3.5 w-3.5" aria-hidden="true" />
              {t("userDialog.revokeSubscription")}
            </button>
          </div>
        )}
      </div>
      <span className="ms-auto" />
      <ToolButton icon={<TrashIcon />} label={String(t("delete"))} tone="red" iconOnly onClick={() => setDeletingUser(user)} />
    </div>
  );
};

const DAY = 86400;

const UserRow: FC<{ user: User; showAdmin: boolean }> = ({ user, showAdmin }) => {
  const { t } = useTranslation();
  const protocols = Object.keys(user.proxies);
  const hasLimit = !!user.data_limit;
  const usedPercent = hasLimit ? Math.min((user.used_traffic / (user.data_limit as number)) * 100, 100) : 0;
  const expiryInfo = relativeExpiryDate(user.expire);
  const lastSeen = lastSeenOf(user.online_at, user.online);

  // Days left, against the length of the period the user was given (creation to expiry),
  // capped to a sensible bar when the period is long or unknown.
  const nowSec = Date.now() / 1000;
  const daysLeft = user.expire ? Math.max(0, (user.expire - nowSec) / DAY) : null;
  const periodDays = user.expire ? Math.max(1, (user.expire - Date.parse(user.created_at) / 1000) / DAY) : null;
  const periodMax = periodDays && Number.isFinite(periodDays) ? Math.min(periodDays, 365) : 30;
  const expiringSoon = daysLeft !== null && daysLeft <= 3;

  // A connected customer is shown green regardless of status, since presence is
  // the more immediate signal; otherwise the card carries its status colour, so
  // the edge always matches the badge and the list can be read by colour alone.
  const statusToneOf = statusTone[user.status] ?? "gray";
  const cardTone: CardTone =
    lastSeen.kind === "online"
      ? "green"
      : statusToneOf === "red"
      ? "red"
      : lastSeen.kind === "never"
      ? "brand"
      : lastSeen.kind === "seen"
      ? "sky"
      : statusToneOf;

  const ringZones = user.status === "limited" ? solidZones(GAUGE_BAD) : LOAD_ZONES;

  return (
    <Card tilt tiltMax={2} className={classNames("flex min-w-0 flex-col gap-3 p-4", cardToneClasses[cardTone])}>
      {/* header: who, how they are, and what protocols */}
      <div className="flex flex-col gap-1.5">
        <div className="flex items-start justify-between gap-2">
          <CardTitle className="min-w-0 break-all text-base" dir="ltr">
            {user.username}
          </CardTitle>
          <StatusPill tone={statusToneOf} live={lastSeen.kind === "online"} className="shrink-0">
            {t(`status.${user.status}`)}
          </StatusPill>
        </div>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="flex items-center gap-1.5">
            <PresenceDot kind={lastSeen.kind} />
            <CardSubtitle className={classNames(lastSeen.kind === "online" && "text-emerald-400")}>
              {lastSeen.kind === "online" && t("rapido.onlineNowLabel")}
              {lastSeen.kind === "never" && t("rapido.neverConnected")}
              {lastSeen.kind === "seen" &&
                t("rapido.lastSeen", {
                  time: ltrIsolate(lastSeen.time || t("rapido.underAMinute")),
                })}
            </CardSubtitle>
          </span>
          {user.synced_from_panel_name && (
            <Badge tone="sky" className="px-1.5 py-0.5 text-[10px]">
              {t("rapido.gateway.syncedFrom", { name: user.synced_from_panel_name })}
            </Badge>
          )}
          {protocols.map((protocol) => (
            <Badge key={protocol} tone="brand" className="px-1.5 py-0.5 text-[10px]">
              {protocol}
            </Badge>
          ))}
        </div>
      </div>

      {/* one plate, two zones: what they used, and how long they have left */}
      <Cluster fixed className="grid-cols-1" aria-label={user.username}>
        <ClusterZone title={t("rapido.users.trafficUsed")} className="p-3">
          <div className="flex items-center gap-3">
            {hasLimit ? (
              <ResourceMeter value={usedPercent} max={100} unit="%" size={88} ariaLabel={String(t("rapido.users.trafficUsed"))} zones={ringZones} />
            ) : (
              <div className="flex h-[88px] w-[88px] shrink-0 items-center justify-center rounded-full border border-dashed border-rapido-border text-2xl text-rapido-muted" aria-label={String(t("rapido.unlimited"))}>
                ∞
              </div>
            )}
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <span className={classNames(TYPE.readout, "text-lg")} dir="ltr">
                {formatBytes(user.used_traffic)}
              </span>
              <span className={TYPE.caption}>
                {hasLimit ? (
                  <>
                    {t("rapido.users.ofLimit")} <span dir="ltr">{formatBytes(user.data_limit as number)}</span>
                  </>
                ) : (
                  t("rapido.unlimited")
                )}
              </span>
              {hasLimit && <SegmentBar value={usedPercent} max={100} count={20} className="h-1.5" zones={ringZones} />}
            </div>
          </div>
        </ClusterZone>
        <ClusterZone title={t("rapido.users.expiry")} className="p-3">
          <div className="flex flex-col gap-1.5">
            <span className={classNames("text-sm font-medium", expiryInfo.status === "expired" ? "text-red-400" : expiringSoon ? "text-amber-400" : "text-rapido-text")}>
              {expiryInfo.status === "expires" && t("expires", { time: ltrIsolate(expiryInfo.time) })}
              {expiryInfo.status === "expired" && t("expired", { time: ltrIsolate(expiryInfo.time) })}
              {!expiryInfo.status && t("rapido.users.noExpiry")}
            </span>
            {daysLeft !== null && (
              <SegmentBar
                value={daysLeft}
                max={periodMax}
                count={24}
                className="h-1.5"
                tint="value"
                zones={[
                  { upTo: 0.1, color: GAUGE_BAD },
                  { upTo: 0.25, color: GAUGE_WARN },
                  { upTo: 1, color: GAUGE_GOOD },
                ]}
              />
            )}
            <div className={classNames(TYPE.caption, "flex flex-wrap items-center gap-x-3 gap-y-0.5")}>
              <span>
                {t("rapido.totalLabel", { value: ltrIsolate(String(formatBytes(user.lifetime_used_traffic))) })}
              </span>
              {showAdmin && user.admin && (
                <span dir="ltr" title={String(t("rapido.filterByAdmin"))}>
                  @{user.admin.username}
                </span>
              )}
            </div>
          </div>
        </ClusterZone>
      </Cluster>

      <div className="mt-auto border-t border-rapido-border/60 pt-3">
        <UserActions user={user} />
      </div>
    </Card>
  );
};

/** The headline counters above the list, as segmented counters in the same language. */
const UsersSummary: FC<{ shown: number; total: number }> = ({ shown, total }) => {
  const { t } = useTranslation();
  const { data: stats } = useSystemStatsQuery();
  if (!stats) return null;
  const all = Math.max(1, stats.total_user);
  const items: { key: string; label: string; value: number; color: string }[] = [
    { key: "active", label: t("status.active"), value: stats.users_active, color: GAUGE_GOOD },
    { key: "limited", label: t("status.limited"), value: stats.users_limited, color: GAUGE_BAD },
    { key: "expired", label: t("status.expired"), value: stats.users_expired, color: GAUGE_VIOLET },
    { key: "online", label: t("rapido.onlineNow"), value: stats.online_users, color: GAUGE_CYAN },
  ];
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5" aria-label={String(t("rapido.users.clusterLabel"))}>
      <div className="flex flex-col gap-1.5 rounded-xl2 border border-rapido-border/70 bg-rapido-surface/60 px-3.5 py-2.5">
        <span className={TYPE.caption}>{t("rapido.users.shown")}</span>
        <span className={classNames(TYPE.readout, "text-xl")} dir="ltr">
          {shown}
          <span className="text-sm font-normal text-rapido-muted"> / {total}</span>
        </span>
        <SegmentBar value={shown} max={Math.max(1, total)} count={16} className="h-1" zones={solidZones(GAUGE_CYAN)} />
      </div>
      {items.map((it) => (
        <div key={it.key} className="flex flex-col gap-1.5 rounded-xl2 border border-rapido-border/70 bg-rapido-surface/60 px-3.5 py-2.5">
          <span className={TYPE.caption}>{it.label}</span>
          <span className={classNames(TYPE.readout, "text-xl")} dir="ltr">
            {it.value}
          </span>
          <SegmentBar value={it.value} max={all} count={16} className="h-1" zones={solidZones(it.color)} />
        </div>
      ))}
    </div>
  );
};

export const UsersTable: FC = () => {
  const { t } = useTranslation();
  const filters = useUsersUiStore((s) => s.filters);
  const setFilters = useUsersUiStore((s) => s.setFilters);
  const setCreatingNewUser = useUsersUiStore((s) => s.setCreatingNewUser);

  const { data, isLoading } = useUsersQuery(filters);
  const isSudo = useIsSudo();
  // /admins is sudo-only, so a reseller's session never asks for it.
  const { data: admins } = useAdminsQuery({ enabled: isSudo });
  const users = data?.users ?? [];
  const total = data?.total ?? 0;

  const [searchInput, setSearchInput] = useState(filters.search ?? "");

  const debouncedSearch = useMemo(
    () =>
      debounce((value: string) => {
        setFilters({ search: value, offset: 0 });
      }, 300),
    [setFilters]
  );

  useEffect(() => {
    return () => debouncedSearch.cancel();
  }, [debouncedSearch]);

  const limit = filters.limit || 10;
  const offset = filters.offset || 0;
  const rangeStart = total === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + limit, total);
  const pageCount = Math.max(1, Math.ceil(total / limit));
  const currentPage = Math.floor(offset / limit) + 1;
  const pageItems = useMemo(
    () => buildPageItems(currentPage, pageCount),
    [currentPage, pageCount]
  );

  return (
    <div className="flex flex-col gap-4">
      <UsersSummary shown={users.length} total={total} />

      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="text"
          value={searchInput}
          placeholder={t("rapido.searchUsers")}
          className="min-w-[220px] flex-1"
          onChange={(e) => {
            const value = e.target.value;
            setSearchInput(value);
            debouncedSearch(value);
          }}
        />

        <Select
          value={filters.status ?? ""}
          onChange={(e) =>
            setFilters({
              status: (e.target.value || undefined) as UsersFilters["status"],
              offset: 0,
            })
          }
        >
          {statusFilterOptions.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {t(opt.labelKey)}
            </option>
          ))}
        </Select>

        {isSudo && (
          <Select
            value={filters.admin ?? ""}
            aria-label={t("rapido.filterByAdmin")}
            onChange={(e) =>
              setFilters({ admin: e.target.value || undefined, offset: 0 })
            }
          >
            <option value="">{t("rapido.allAdmins")}</option>
            {(admins ?? []).map((a) => (
              <option key={a.username} value={a.username}>
                {a.username}
              </option>
            ))}
          </Select>
        )}

        <Select
          value={filters.sort}
          onChange={(e) => setFilters({ sort: e.target.value })}
        >
          {sortOptions.map((opt) => (
            <option key={opt.value} value={opt.value}>
              {t(opt.labelKey)}
            </option>
          ))}
        </Select>

        {/* ms-auto, not ml-auto: pushing an item to the end of a flex row means
            an auto margin on its *start* side. In RTL the free space is already
            on the left, so margin-left:auto absorbed nothing. */}
        <Button variant="secondary" className="ms-auto shrink-0" onClick={() => void downloadUsersCsv(filters).catch(() => undefined)}>
          {t("rapido.users.exportCsv")}
        </Button>
        <Button
          variant="primary"
          className="shrink-0"
          onClick={() => setCreatingNewUser(true)}
        >
          <span aria-hidden="true">+ </span>
          {t("rapido.newUser")}
        </Button>
      </div>

      {isLoading && (
        <div className="py-12 text-center text-sm text-rapido-muted">
          {t("rapido.loadingUsers")}
        </div>
      )}

      {!isLoading && users.length === 0 && (
        <div className="py-12 text-center text-sm text-rapido-muted">
          {t("usersTable.noUserMatched")}
        </div>
      )}

      {!isLoading && users.length > 0 && (
        <div className="grid grid-cols-[repeat(auto-fill,minmax(min(21rem,100%),1fr))] gap-4">
          {users.map((user) => (
            <UserRow key={user.username} user={user} showAdmin={isSudo} />
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-3 text-xs text-rapido-muted">
        <div className="flex items-center gap-3">
          <span>
            {t("rapido.range", { start: rangeStart, end: rangeEnd, total })}
          </span>
          <label className="flex items-center gap-1.5">
            {t("itemsPerPage")}
            <Select
              value={limit}
              className="px-2 py-1 text-xs"
              onChange={(e) => {
                const next = Number(e.target.value);
                // Remembered across sessions, same preference the old
                // dashboard used, so switching views doesn't reset it.
                setUsersPerPageLimitSize(String(next));
                setFilters({ limit: next, offset: 0 });
              }}
            >
              {PAGE_SIZES.map((size) => (
                <option key={size} value={size}>
                  {size}
                </option>
              ))}
            </Select>
          </label>
        </div>

        <div className="flex flex-wrap items-center gap-1.5">
          <Button
            variant="chip"
            disabled={currentPage === 1}
            onClick={() => setFilters({ offset: Math.max(offset - limit, 0) })}
          >
            {t("previous")}
          </Button>

          {pageItems.map((item, index) =>
            item === "gap" ? (
              <span key={`gap-${index}`} className="px-1">
                …
              </span>
            ) : (
              <Button
                key={item}
                variant="chip"
                tone={item === currentPage ? "accent" : "neutral"}
                aria-current={item === currentPage ? "page" : undefined}
                className="min-w-[2rem]"
                onClick={() => setFilters({ offset: (item - 1) * limit })}
              >
                {item}
              </Button>
            )
          )}

          <Button
            variant="chip"
            disabled={currentPage >= pageCount}
            onClick={() => setFilters({ offset: offset + limit })}
          >
            {t("next")}
          </Button>
        </div>
      </div>
    </div>
  );
};

export default UsersTable;
