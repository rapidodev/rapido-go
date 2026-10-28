import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { Card } from "rapido-ui/Card";
import { Button } from "rapido-ui/Button";
import { Modal } from "rapido-ui/Modal";
import { errorText } from "service/errors";
type Snapshot = { id: number; created_at: string; reason: string; config?: unknown };
export const CoreSnapshots = () => {
 const {t}=useTranslation();const client=useQueryClient();const [open,setOpen]=useState(false);const [selected,setSelected]=useState<Snapshot|null>(null);const [saving,setSaving]=useState(false);const [error,setError]=useState("");
 const history=useQuery({queryKey:["core-snapshots"],queryFn:()=>fetch<Snapshot[]>("/core/config/backups"),enabled:open});
 const preview=async(id:number)=>{setError("");try{setSelected(await fetch<Snapshot>(`/core/config/backups/${id}`))}catch(e){setError(errorText(e,t("rapido.snapshots.failed")))}};
 const restore=async()=>{if(!selected)return;setSaving(true);setError("");try{await fetch(`/core/config/backups/${selected.id}/restore`,{method:"POST",body:{confirm:true}});setSelected(null);await client.invalidateQueries()}catch(e){setError(errorText(e,t("rapido.snapshots.failed")))}finally{setSaving(false)}};
 return <Card><Button variant="chip" onClick={()=>{setOpen(!open);if(!open)void history.refetch()}}>{t("rapido.snapshots.title")}</Button>
 {open&&<div className="mt-3 flex flex-col gap-2">{history.isError&&<p role="alert">{t("rapido.snapshots.failed")}</p>}{history.data?.map(row=><Button key={row.id} variant="chip" onClick={()=>void preview(row.id)}>#{row.id} ? {row.created_at}</Button>)}</div>}
 {error&&<p role="alert">{error}</p>}
 {selected&&<Modal onClose={()=>{if(!saving)setSelected(null)}}><h2>{t("rapido.snapshots.preview")}</h2><pre dir="ltr" className="max-h-80 overflow-auto whitespace-pre-wrap text-xs">{JSON.stringify(selected.config,null,2)}</pre><p className="my-3">{t("rapido.snapshots.warning")}</p><Button variant="chip" disabled={saving} onClick={()=>void restore()}>{t("rapido.snapshots.restore")}</Button></Modal>}
 </Card>;
};
