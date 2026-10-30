import { describe, expect, it } from "vitest";
import { buildNodeInstallCommand } from "../nodeInstall";

describe("buildNodeInstallCommand", () => {
  it("is exactly the documented one-liner, built from the panel's origin", () => {
    expect(buildNodeInstallCommand("https://panel.example.com")).toBe(
      "curl -fsSL https://panel.example.com/install/node.sh | sudo bash"
    );
  });

  it("keeps a port in the origin and never doubles a trailing slash", () => {
    expect(buildNodeInstallCommand("https://203.0.113.9:8000")).toBe(
      "curl -fsSL https://203.0.113.9:8000/install/node.sh | sudo bash"
    );
    expect(buildNodeInstallCommand("https://panel.example.com/")).toBe(
      "curl -fsSL https://panel.example.com/install/node.sh | sudo bash"
    );
  });
});
