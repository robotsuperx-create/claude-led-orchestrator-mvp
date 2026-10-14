import { describe, expect, it } from "vitest";
import { isCloudSignInConfigured } from "./cloud-session";

describe("isCloudSignInConfigured", () => {
  it("is off by default: X-ROBOT-AI ships no upstream WorkOS client id", () => {
    // Agent Orchestrator's cloud sign-in is not offered; an owner who runs
    // their own WorkOS project configures VITE_WORKOS_CLIENT_ID.
    expect(isCloudSignInConfigured(undefined)).toBe(false);
    expect(isCloudSignInConfigured("client_owner")).toBe(true);
  });

  it("treats an explicitly blank client ID as not configured", () => {
    expect(isCloudSignInConfigured("   ")).toBe(false);
  });

  it("shows Cloud sign-in when the WorkOS client ID is configured", () => {
    expect(isCloudSignInConfigured("client_123")).toBe(true);
  });
});
