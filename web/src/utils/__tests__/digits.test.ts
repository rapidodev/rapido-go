import { describe, expect, it } from "vitest";
import { toLatinDigits } from "utils/digits";

describe("toLatinDigits", () => {
  it("reads what a Persian or Arabic keyboard types as the digits they are", () => {
    expect(toLatinDigits("۱۲۳۴۵۶۷۸۹۰")).toBe("1234567890");
    expect(toLatinDigits("٠١٢٣٤٥٦٧٨٩")).toBe("0123456789");
  });

  it("leaves everything else alone", () => {
    expect(toLatinDigits("-443 a۴b")).toBe("-443 a4b");
    expect(toLatinDigits("")).toBe("");
  });
});
