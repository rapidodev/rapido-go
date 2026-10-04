import { FC } from "react";
import { useTranslation } from "react-i18next";
import { RapidoShell } from "rapido-ui/Shell";
import { TunnelRelaysAdmin } from "rapido-ui/TunnelRelaysAdmin";

export const TunnelRelaysPage: FC = () => {
  const { t } = useTranslation();

  return (
    <RapidoShell active="tunnelRelays">
      <div className="p-6">
        <h1 className="mb-1 text-2xl font-bold">{t("rapido.tunnelRelays.title")}</h1>
        <p className="mb-6 text-sm text-rapido-muted">{t("rapido.tunnelRelays.subtitle")}</p>
        <TunnelRelaysAdmin />
      </div>
    </RapidoShell>
  );
};

export default TunnelRelaysPage;
