// The one-line command that installs a freshly created node, from the installer
// this very panel serves. The node's one-time setup blob is deliberately not in
// it: the installer asks for that on the terminal, hidden, which keeps the node's
// private key out of shell history and the process list.
export const buildNodeInstallCommand = (origin: string): string => {
  const base = origin.replace(/\/+$/, "");
  return `curl -fsSL ${base}/install/node.sh | sudo bash`;
};
