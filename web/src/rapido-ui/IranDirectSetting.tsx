import { FC, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { errorText } from "service/errors";
import { IntegrationSettingsStatus } from "types/Integrations";
import { Card } from "rapido-ui/Card";
import { Badge } from "rapido-ui/Badge";
import { Button } from "rapido-ui/Button";
import { Checkbox } from "rapido-ui/Checkbox";

// "Send Iranian sites and IPs direct" (subscription_iran_direct on PUT
// /api/settings/integrations). Saved the moment it is toggled: one switch
// needs no separate Save button, and the PATCH body names only this key, so
// it cannot touch anything the integrations form below holds unsaved.
// Nothing disconnects when it changes - apps pick it up at their next
// subscription update - so it needs no confirmation either.
export const IranDirectSetting: FC = () => {
  const { t } = useTranslation();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [loadError, setLoadError] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    fetch<IntegrationSettingsStatus>("/settings/integrations")
      .then((r) => {
        setEnabled(r.subscription_iran_direct);
        setLoadError("");
      })
      .catch((e) => setLoadError(errorText(e, t("rapido.iranDirect.loadFailed"))));
  }, [t]);

  // null sends the key as null: back to SUBSCRIPTION_IRAN_DIRECT.
  const save = (value: boolean | null) => {
    setSaving(true);
    setSaveError("");
    setSaved(false);
    fetch<IntegrationSettingsStatus>("/settings/integrations", {
      method: "PUT",
      body: { subscription_iran_direct: value },
    })
      .then((r) => {
        setEnabled(r.subscription_iran_direct);
        setSaved(true);
      })
      .catch((e) => setSaveError(errorText(e, t("rapido.iranDirect.saveFailed"))))
      .finally(() => setSaving(false));
  };

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center gap-2">
        <span className="text-sm font-semibold">{t("rapido.iranDirect.title")}</span>
        {enabled !== null && (
          <Badge tone={enabled ? "green" : "gray"}>
            {enabled ? t("rapido.integrations.enabled") : t("rapido.integrations.disabled")}
          </Badge>
        )}
      </div>

      {loadError ? (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {loadError}
        </div>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-2">
          <Checkbox
            label={t("rapido.iranDirect.label")}
            checked={enabled === true}
            disabled={enabled === null || saving}
            onChange={(e) => save(e.target.checked)}
          />
          <Button variant="chip" tone="neutral" disabled={enabled === null || saving} onClick={() => save(null)}>
            {t("rapido.integrations.resetToEnv")}
          </Button>
        </div>
      )}

      <div className="mt-3 flex flex-col gap-1.5 text-xs text-rapido-muted">
        <p>{t("rapido.iranDirect.hint")}</p>
        <p>{t("rapido.iranDirect.apps")}</p>
        <p className="text-amber-400">{t("rapido.iranDirect.manualApps")}</p>
        <p>{t("rapido.iranDirect.offHint")}</p>
      </div>

      {saveError && (
        <div className="mt-3 rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {saveError}
        </div>
      )}
      {saved && (
        <div className="mt-3 rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-400">
          {t("rapido.iranDirect.saved")}
        </div>
      )}
    </Card>
  );
};

export default IranDirectSetting;
