import { FC, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Cluster, ClusterZone } from "./Cluster";
import { ResourceMeter } from "./ResourceMeter";

export type ResourceItem = {
  key: string;
  label: string;
  value: number | null | undefined;
  sublabel?: ReactNode;
  /** Defaults to 100 (a percentage). */
  max?: number;
  unit?: string;
};

/**
 * A host's resources as one small instrument cluster: a plate with a quiet
 * title and a row of equal rings (or segmented bars, per Appearance). The same
 * plate the tunnel card uses, so Overview, Monitoring and Nodes read as the
 * same instrument.
 */
export const ResourceStrip: FC<{
  items: ResourceItem[];
  title?: ReactNode;
  chips?: ReactNode;
  size?: number;
}> = ({ items, title, chips, size = 92 }) => {
  const { t } = useTranslation();
  return (
    <Cluster minZone="0px">
      <ClusterZone title={title ?? String(t("rapido.tunnels.resources"))} chips={chips} className="p-3">
        <div className="grid justify-items-center gap-x-2 gap-y-3" style={{ gridTemplateColumns: `repeat(${items.length}, minmax(0, 1fr))` }}>
          {items.map((it) => (
            <ResourceMeter
              key={it.key}
              value={it.value ?? null}
              max={it.max ?? 100}
              unit={it.unit ?? "%"}
              label={it.label}
              sublabel={it.sublabel}
              size={size}
            />
          ))}
        </div>
      </ClusterZone>
    </Cluster>
  );
};
