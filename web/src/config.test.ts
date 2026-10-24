import { describe, expect, it } from "vitest";
import { resolveShippingApiBase } from "./config";

describe("resolveShippingApiBase", () => {
  it("is this context's own /api/shipping-and-tracking under the runtime origin", () => {
    expect(resolveShippingApiBase({ apiOrigin: "http://localhost:8000" }, true)).toBe(
      "http://localhost:8000/api/shipping-and-tracking",
    );
  });

  it("drops trailing slashes of the origin", () => {
    expect(resolveShippingApiBase({ apiOrigin: "http://kong.local///" }, false)).toBe(
      "http://kong.local/api/shipping-and-tracking",
    );
  });

  it("falls back to the developer port outside production", () => {
    expect(resolveShippingApiBase({}, false)).toBe("http://localhost:8080");
  });

  it("refuses to guess in production", () => {
    expect(() => resolveShippingApiBase({}, true)).toThrow(/apiOrigin is required/);
    expect(() => resolveShippingApiBase({ apiOrigin: "" }, true)).toThrow(/apiOrigin is required/);
  });
});
