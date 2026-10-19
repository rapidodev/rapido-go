import { FC } from "react";
import { useTranslation } from "react-i18next";
import { RapidoShell } from "rapido-ui/Shell";
import { AppearancePanel } from "rapido-ui/AppearancePanel";
import { Card } from "rapido-ui/Card";

export const AppearancePage: FC = () => {
  const { t } = useTranslation();

  return (
    <RapidoShell active="appearance">
      <div className="p-4 sm:p-6">
        <h1 className="mb-1 text-2xl font-bold">{t("rapido.appearance.title")}</h1>
        <p className="mb-6 text-sm text-rapido-muted">{t("rapido.appearance.subtitle")}</p>
        <Card className="max-w-4xl">
          <AppearancePanel />
        </Card>
      </div>
    </RapidoShell>
  );
};

export default AppearancePage;
