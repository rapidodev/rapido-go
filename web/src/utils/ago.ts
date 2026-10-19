// "N <unit> ago" phrasing, shared by every card that says when something was
// last seen. Reuses the strings the Monitoring page established.
const agoLabelKey = {
  seconds: "rapido.monitoring.agoSeconds",
  minutes: "rapido.monitoring.agoMinutes",
  hours: "rapido.monitoring.agoHours",
  days: "rapido.monitoring.agoDays",
} as const;

export const agoText = (
  t: (key: string, opts?: Record<string, unknown>) => unknown,
  iso: string,
  now: number = Date.now()
): string => {
  const seconds = Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000));
  if (seconds < 60) return String(t(agoLabelKey.seconds, { value: seconds }));
  if (seconds < 3600) return String(t(agoLabelKey.minutes, { value: Math.floor(seconds / 60) }));
  if (seconds < 86400) return String(t(agoLabelKey.hours, { value: Math.floor(seconds / 3600) }));
  return String(t(agoLabelKey.days, { value: Math.floor(seconds / 86400) }));
};
