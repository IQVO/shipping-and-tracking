import { describe, expect, it } from "vitest";
import { trackingNumberProblem, utcLabel } from "./lib";

describe("utcLabel", () => {
  it("formats an RFC 3339 instant as a UTC second-precision stamp", () => {
    expect(utcLabel("2026-10-09T08:05:30Z")).toBe("2026-10-09 08:05:30Z");
  });

  it("returns the raw text when unparseable and the no-value marker when absent", () => {
    expect(utcLabel("not-a-time")).toBe("not-a-time");
    expect(utcLabel(undefined)).toBe("—");
  });
});

describe("trackingNumberProblem", () => {
  it("accepts a well-formed tracking number", () => {
    expect(trackingNumberProblem("RN0123456789ABCDEF")).toBeNull();
  });

  it("rejects the empty value, control characters and slashes", () => {
    expect(trackingNumberProblem("")).toMatch(/Enter a tracking number/);
    expect(trackingNumberProblem("RN 1")).toMatch(/cannot contain/);
    expect(trackingNumberProblem("RN/1")).toMatch(/cannot contain/);
  });

  it("rejects an over-long tracking number", () => {
    expect(trackingNumberProblem("R".repeat(65))).toMatch(/at most 64/);
  });
});
