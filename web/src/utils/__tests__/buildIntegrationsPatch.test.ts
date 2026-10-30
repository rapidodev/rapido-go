import { describe, expect, it } from "vitest";
import { buildIntegrationsPatch, IntegrationFieldSpec, invalidIntegrationFields } from "../buildIntegrationsPatch";

const fields: IntegrationFieldSpec[] = [
  { key: "telegram_api_token", kind: "password" },
  { key: "telegram_admin_ids", kind: "intArray" },
  { key: "webhook_addresses", kind: "strArray" },
  { key: "telegram_logger_channel_id", kind: "int" },
  { key: "discord_webhook_url", kind: "text" },
];

describe("buildIntegrationsPatch", () => {
  it("omits a key that was never touched (no draft, not cleared)", () => {
    const body = buildIntegrationsPatch(fields, {}, new Set());
    expect(body).toEqual({});
    expect("discord_webhook_url" in body).toBe(false);
  });

  it("a cleared key wins over a stray draft value for the same field", () => {
    const body = buildIntegrationsPatch(
      fields,
      { discord_webhook_url: "http://leftover-draft.example" },
      new Set(["discord_webhook_url"])
    );
    expect(body.discord_webhook_url).toBeNull();
  });

  it("a non-empty draft for an uncleared text field is sent as-is", () => {
    const body = buildIntegrationsPatch(fields, { discord_webhook_url: "http://new.example" }, new Set());
    expect(body).toEqual({ discord_webhook_url: "http://new.example" });
  });

  it("a draft that trims to empty is treated as untouched, not cleared", () => {
    const body = buildIntegrationsPatch(fields, { discord_webhook_url: "   " }, new Set());
    expect(body).toEqual({});
  });

  it("parses an int field to a number", () => {
    const body = buildIntegrationsPatch(fields, { telegram_logger_channel_id: "12345" }, new Set());
    expect(body).toEqual({ telegram_logger_channel_id: 12345 });
  });

  it("parses a comma-separated intArray field, trimming whitespace and dropping empties", () => {
    const body = buildIntegrationsPatch(
      fields,
      { telegram_admin_ids: " 111, 222,,333 " },
      new Set()
    );
    expect(body).toEqual({ telegram_admin_ids: [111, 222, 333] });
  });

  it("parses a comma-separated strArray field the same way", () => {
    const body = buildIntegrationsPatch(
      fields,
      { webhook_addresses: "https://a.example, https://b.example" },
      new Set()
    );
    expect(body).toEqual({
      webhook_addresses: ["https://a.example", "https://b.example"],
    });
  });

  it("accepts clearedKeys as a plain array as well as a Set", () => {
    const body = buildIntegrationsPatch(fields, {}, ["discord_webhook_url"]);
    expect(body).toEqual({ discord_webhook_url: null });
  });

  it("builds a mixed patch: one cleared, one changed, the rest omitted", () => {
    const body = buildIntegrationsPatch(
      fields,
      { discord_webhook_url: "http://new.example", telegram_api_token: "unused-because-cleared" },
      new Set(["telegram_api_token"])
    );
    expect(body).toEqual({
      discord_webhook_url: "http://new.example",
      telegram_api_token: null,
    });
    expect(Object.keys(body).sort()).toEqual(["discord_webhook_url", "telegram_api_token"]);
  });

  it("reads a Persian keyboard's digits in number fields", () => {
    const body = buildIntegrationsPatch(
      fields,
      { telegram_logger_channel_id: "-۱۰۰۱۲۳", telegram_admin_ids: "۱۱۱, ٢٢٢" },
      new Set()
    );
    expect(body).toEqual({ telegram_logger_channel_id: -100123, telegram_admin_ids: [111, 222] });
  });

  // NaN goes out as JSON null, and a null here means "clear the setting back to .env".
  it("never lets a number field that holds no number clear the setting", () => {
    const body = buildIntegrationsPatch(
      fields,
      { telegram_logger_channel_id: "abc", telegram_admin_ids: "111, abc" },
      new Set()
    );
    expect(body).toEqual({});
    expect(JSON.stringify(body)).not.toContain("null");
  });
});

describe("invalidIntegrationFields", () => {
  it("names the number fields that hold no number", () => {
    const invalid = invalidIntegrationFields(
      fields,
      { telegram_logger_channel_id: "12a", telegram_admin_ids: "1, 2", discord_webhook_url: "anything" },
      new Set()
    );
    expect(invalid.map((f) => f.key)).toEqual(["telegram_logger_channel_id"]);
  });

  it("accepts empty drafts and every digit a keyboard types", () => {
    expect(invalidIntegrationFields(fields, {}, new Set())).toEqual([]);
    expect(invalidIntegrationFields(fields, { telegram_admin_ids: "۱۱۱,, -2 " }, new Set())).toEqual([]);
  });

  it("does not hold a cleared field's stray draft against it", () => {
    expect(invalidIntegrationFields(fields, { telegram_logger_channel_id: "abc" }, ["telegram_logger_channel_id"])).toEqual([]);
  });
});
