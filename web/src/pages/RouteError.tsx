import { FC } from "react";
import { Navigate, useLocation, useNavigate, useRevalidator, useRouteError } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { errorText, isChunkLoadError } from "service/errors";
import { getAuthToken } from "utils/authStorage";
import { dirOf } from "utils/language";
import { Button } from "rapido-ui/Button";
import { Card } from "rapido-ui/Card";
import "rapido-ui/tailwind.css";

// What every route shows when its loader or its page fails. A panel that could
// not be reached, a page that threw, or a chunk a deploy has replaced say
// nothing about the session, so the token stays and the admin stays signed in;
// only a 401 ends a session (service/http.ts), and by the time it gets here it
// has already cleared the token.
export const RouteError: FC = () => {
  const error = useRouteError();
  const { t, i18n } = useTranslation();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const revalidator = useRevalidator();

  if (!getAuthToken()) return <Navigate to="/login" replace />;

  const staleChunk = isChunkLoadError(error);
  const retrying = revalidator.state === "loading";

  return (
    <div
      dir={dirOf(i18n.language)}
      lang={i18n.language}
      className="relative flex min-h-screen w-full items-center justify-center p-4 font-sans text-rapido-text sm:p-6"
    >
      <Card elevation={3} role="alert" className="w-full max-w-[420px] animate-rp-rise p-6">
        <h1 className="text-lg font-semibold">
          {t(staleChunk ? "rapido.routeError.chunkTitle" : "rapido.routeError.title")}
        </h1>
        <p className="mt-2 text-sm text-rapido-muted">
          {t(staleChunk ? "rapido.routeError.chunkBody" : "rapido.routeError.body")}
        </p>
        {!staleChunk && (
          <p dir="ltr" className="mt-2 break-words text-xs text-rapido-muted">
            {errorText(error, "")}
          </p>
        )}
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <Button variant="secondary" onClick={() => navigate("/login")}>
            {t("header.logout")}
          </Button>
          {pathname !== "/" && (
            <Button variant="secondary" onClick={() => navigate("/")}>
              {t("rapido.overview")}
            </Button>
          )}
          {/* A rejected lazy() stays rejected until the page is loaded again;
              anything else can be retried without leaving the page, which also
              works while the connection is still down. */}
          {staleChunk ? (
            <Button variant="primary" onClick={() => window.location.reload()}>
              {t("rapido.routeError.reload")}
            </Button>
          ) : (
            <Button variant="primary" disabled={retrying} onClick={() => void revalidator.revalidate()}>
              {retrying ? t("rapido.pleaseWait") : t("rapido.retry")}
            </Button>
          )}
        </div>
      </Card>
    </div>
  );
};

export default RouteError;
