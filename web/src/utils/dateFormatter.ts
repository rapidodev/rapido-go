import dayjs from "dayjs";

type DurationUnit = "year" | "month" | "day" | "hour" | "minute";

// Intl gives a properly localised "2 days" / "۲ روز" / "2 дня" / "2天" for free,
// so these need no plural keys of their own in four languages. Cached like the
// formatters in ticketHelpers.ts: a list of 200 rows would otherwise build
// hundreds of them per render.
const formatters = new Map<string, unknown>();

const cached = <T,>(key: string, locale: string, build: (locale: string | undefined) => T): T => {
  if (!formatters.has(key)) {
    let formatter: T;
    try {
      formatter = build(locale);
    } catch {
      // An unexpected/invalid tag from the language detector must not blank the page.
      formatter = build(undefined);
    }
    formatters.set(key, formatter);
  }
  return formatters.get(key) as T;
};

// Minutes in the short form, as every "N min ago" in the panel has them; it also
// spares Russian the case the long form gets wrong after "через" and before "назад".
const formatUnit = (locale: string, unit: DurationUnit, value: number): string =>
  cached(
    `${locale}:${unit}`,
    locale,
    (l) => new Intl.NumberFormat(l, { style: "unit", unit, unitDisplay: unit === "minute" ? "short" : "long" })
  ).format(Math.abs(value));

const joinUnits = (locale: string, parts: string[]): string =>
  cached(`${locale}:list`, locale, (l) => new Intl.ListFormat(l, { style: "short", type: "unit" })).format(parts);

export const relativeExpiryDate = (expiryDate: number | null | undefined, locale = "en") => {
  let dateInfo = { status: "", time: "" };
  if (expiryDate) {
    if (
      dayjs(expiryDate * 1000)
        .utc()
        .isAfter(dayjs().utc())
    ) {
      dateInfo.status = "expires";
    } else {
      dateInfo.status = "expired";
    }
    const durationSlots: string[] = [];
    const duration = dayjs.duration(
      dayjs(expiryDate * 1000)
        .utc()
        .diff(dayjs())
    );
    if (duration.years() != 0) durationSlots.push(formatUnit(locale, "year", duration.years()));
    if (duration.months() != 0) durationSlots.push(formatUnit(locale, "month", duration.months()));
    if (duration.days() != 0) durationSlots.push(formatUnit(locale, "day", duration.days()));
    if (durationSlots.length === 0) {
      if (duration.hours() != 0) durationSlots.push(formatUnit(locale, "hour", duration.hours()));
      if (duration.minutes() != 0) durationSlots.push(formatUnit(locale, "minute", duration.minutes()));
    }
    dateInfo.time = joinUnits(locale, durationSlots);
  }
  return dateInfo;
};
