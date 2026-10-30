import { FC, ReactNode } from "react";
import { Navigate } from "react-router-dom";
import { useCurrentAdminQuery, useIsSudo } from "hooks/useCurrentAdminQuery";

// Extracted out of the old pages/Router.tsx (it was declared inline there,
// specific to the old page list). Sudo gate shared by every page a reseller
// must not reach: Hosts, Admins, Integrations. The backend already answers
// 403 for those endpoints; this only hides a page a non-sudo admin could do
// nothing with anyway.
export const SudoOnly: FC<{ children: ReactNode }> = ({ children }) => {
  const { isPending } = useCurrentAdminQuery();
  const isSudo = useIsSudo();

  // is_sudo defaults to false until the admin query resolves, so redirecting
  // during the pending window would bounce a legitimate sudo admin.
  if (isPending) return null;

  return isSudo ? <>{children}</> : <Navigate to="/" replace />;
};

export default SudoOnly;
