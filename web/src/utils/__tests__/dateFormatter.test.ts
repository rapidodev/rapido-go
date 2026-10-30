import dayjs from "dayjs";
import Duration from "dayjs/plugin/duration";
import utc from "dayjs/plugin/utc";
import { afterAll, beforeAll, describe, expect, it, vi } from "vitest";
import { relativeExpiryDate } from "utils/dateFormatter";

// main.tsx registers these; dayjs() reads the real clock, so it is pinned.
dayjs.extend(utc);
dayjs.extend(Duration);

const NOW = Date.parse("2026-09-25T12:00:00Z");
beforeAll(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});
afterAll(() => {
  vi.useRealTimers();
});

const inHours = (hours: number) => Math.floor(NOW / 1000) + hours * 3600;

describe("relativeExpiryDate", () => {
  it("says which side of the date we are on, whatever the language", () => {
    expect(relativeExpiryDate(inHours(72), "fa").status).toBe("expires");
    expect(relativeExpiryDate(inHours(-72), "fa").status).toBe("expired");
    expect(relativeExpiryDate(null).status).toBe("");
  });

  it("keeps the English it always had, unless the language says otherwise", () => {
    expect(relativeExpiryDate(inHours(72)).time).toBe("3 days");
    expect(relativeExpiryDate(inHours(24)).time).toBe("1 day");
    expect(relativeExpiryDate(inHours(-3)).time).toBe("3 hours");
    expect(relativeExpiryDate(inHours(1) + 20 * 60, "en").time).toBe("1 hour, 20 min");
  });

  it("writes the units in the admin's language, not English dropped into a translated sentence", () => {
    expect(relativeExpiryDate(inHours(72), "fa").time).toBe("۳ روز");
    expect(relativeExpiryDate(inHours(72), "ru").time).toBe("3 дня");
    expect(relativeExpiryDate(inHours(72), "zh").time).toBe("3天");
    for (const lang of ["fa", "ru", "zh"]) {
      for (const hours of [24 * 400, 24 * 40, 72, 3]) {
        expect(relativeExpiryDate(inHours(hours), lang).time, `${lang} ${hours}`).not.toMatch(/[a-z]/i);
      }
    }
  });

  it("does not blank the page on a language tag Intl refuses", () => {
    expect(relativeExpiryDate(inHours(72), "not a language").time).toContain("3");
  });
});
