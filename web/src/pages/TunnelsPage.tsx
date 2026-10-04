import { FC } from "react";
import { useTranslation } from "react-i18next";
import { RapidoShell } from "rapido-ui/Shell";
import { TunnelsAdmin } from "rapido-ui/TunnelsAdmin";

export const TunnelsPage: FC = () => {
  const { t } = useTranslation();

  return (
    <RapidoShell active="tunnels">
      <div className="p-6">
        <h1 className="mb-1 text-2xl font-bold">{t("rapido.tunnels.title")}</h1>
        <p className="mb-6 text-sm text-rapido-muted">{t("rapido.tunnels.subtitle")}</p>
        <TunnelsAdmin />
      </div>
    </RapidoShell>
  );
};

export default TunnelsPage;
