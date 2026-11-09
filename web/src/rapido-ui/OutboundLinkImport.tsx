import { FC, useState } from "react";
import { ArrowDownTrayIcon } from "@heroicons/react/24/outline";
import { useTranslation } from "react-i18next";
import { Item } from "utils/coreConfigRules";
import { LinkError, LinkNoteKind, parseOutboundLink } from "utils/outboundLink";
import { Button } from "./Button";
import { Input } from "./Input";
import { autoIsolate, ltrIsolate } from "./bidi";

// The share-link box at the top of a plain outbound's JSON tab: paste a client link and the
// editor below gets the same outbound in Rapido's own format. It only fills the editor; the
// dialog's Save still decides, and a link that can't be imported leaves the editor as it was.

type Translate = (key: string, opts?: Record<string, unknown>) => string;

const PLACEHOLDER = "vmess:// vless:// trojan:// ss:// hysteria2://";

export const OutboundLinkImport: FC<{ onImport: (outbound: Item) => void }> = ({ onImport }) => {
  const { t: rawT } = useTranslation();
  const t = rawT as Translate;
  const [link, setLink] = useState("");
  const [error, setError] = useState<LinkError | null>(null);
  const [filled, setFilled] = useState<{ name: string; ignored: string[]; notes: LinkNoteKind[] } | null>(null);

  const fill = () => {
    const result = parseOutboundLink(link);
    if (!result.ok) {
      setError(result.error);
      setFilled(null);
      return;
    }
    onImport(result.value.outbound);
    setError(null);
    setFilled({ name: result.value.name, ignored: result.value.ignored, notes: result.value.notes });
    // The link holds the credentials, which are in the editor now.
    setLink("");
  };

  return (
    <div className="mb-3 flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <label htmlFor="core-link-import" className="shrink-0 text-xs font-medium text-rapido-text">
          {t("rapido.coreForm.link.label")}
        </label>
        <Input
          id="core-link-import"
          dir="ltr"
          autoComplete="off"
          spellCheck={false}
          className="min-w-0 flex-1"
          placeholder={PLACEHOLDER}
          hasError={!!error}
          aria-invalid={!!error}
          value={link}
          onChange={(e) => {
            setLink(e.target.value);
            setError(null);
          }}
          onKeyDown={(e) => {
            if (e.key !== "Enter") return;
            e.preventDefault();
            fill();
          }}
        />
        <Button
          variant="chip"
          tone="accent"
          className="shrink-0 self-stretch"
          aria-label={t("rapido.coreForm.link.fill")}
          title={t("rapido.coreForm.link.fill")}
          onClick={fill}
        >
          <ArrowDownTrayIcon className="h-4 w-4" aria-hidden="true" />
        </Button>
      </div>
      {error && (
        <p role="alert" className="text-xs text-red-400">
          {t(`rapido.coreForm.link.error.${error.kind}`, { value: ltrIsolate(error.value ?? "") })}
        </p>
      )}
      {filled && (
        <p role="status" className="text-xs text-emerald-400">
          {filled.name !== ""
            ? t("rapido.coreForm.link.filledNamed", { name: autoIsolate(filled.name) })
            : t("rapido.coreForm.link.filled")}
        </p>
      )}
      {filled && filled.ignored.length > 0 && (
        <p className="text-xs text-amber-400">{t("rapido.coreForm.link.ignored", { keys: ltrIsolate(filled.ignored.join(", ")) })}</p>
      )}
      {filled?.notes.map((note) => (
        <p key={note} className="text-xs text-amber-400">
          {t(`rapido.coreForm.link.note.${note}`)}
        </p>
      ))}
    </div>
  );
};
