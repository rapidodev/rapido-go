import { FC } from "react";
import { useTranslation } from "react-i18next";
import { RapidoShell } from "rapido-ui/Shell";
import { ThemeStudio } from "rapido-ui/ThemeStudio";

export const ThemePage: FC = () => {
  const { t } = useTranslation();

  return (
    <RapidoShell active="theme">
      <div className="p-4 sm:p-6">
        <h1 className="mb-1 text-2xl font-bold">{t("rapido.theme.title")}</h1>
        <p className="mb-6 text-sm text-rapido-muted">{t("rapido.theme.subtitle")}</p>
        <ThemeStudio />
      </div>
    </RapidoShell>
  );
};

export default ThemePage;
