import { FC } from "react";
import { useTranslation } from "react-i18next";
import { RapidoShell } from "rapido-ui/Shell";
import { Link } from "react-router-dom";
import { IntegrationsForm } from "rapido-ui/IntegrationsForm";

export const IntegrationsPage: FC = () => {
  const { t } = useTranslation();

  return (
    <RapidoShell active="integrations">
      <div className="p-6">
        <h1 className="mb-1 text-2xl font-bold">{t("rapido.integrations.title")}</h1>
        <p className="mb-3 text-sm text-rapido-muted">{t("rapido.integrations.subtitle")}</p>
        <Link to="/appearance/" className="mb-6 inline-block text-sm text-rapido-accent hover:underline">
          {t("rapido.appearance.settingsLink")}
        </Link>
        <IntegrationsForm />
      </div>
    </RapidoShell>
  );
};

export default IntegrationsPage;
