import { describe, expect, it } from "vitest";
import { getJourney } from "./api";
import { SHIPPING_API_BASE } from "./config";
import { json, mockApi, problem } from "./test/fetchMock";
import { journey } from "./test/fixtures";

describe("getJourney", () => {
  it("GETs /journeys/{trackingNumber} on this context's own base, escaping the id", async () => {
    const api = mockApi({ "GET /journeys/RN0123456789ABCDEF": json(journey()) });
    const got = await getJourney("RN0123456789ABCDEF");
    expect(got.state).toBe("IN_TRANSIT");
    expect(api.to("GET /journeys/RN0123456789ABCDEF")).toHaveLength(1);
    expect(api.calls[0].url).toBe(`${SHIPPING_API_BASE}/journeys/RN0123456789ABCDEF`);
    expect(api.calls[0].method).toBe("GET");
  });

  it("never leaves the scan list undefined", async () => {
    mockApi({ "GET /journeys/RN1": json({ ...journey(), scans: undefined }) });
    const got = await getJourney("RN1");
    expect(got.scans).toEqual([]);
  });

  it("carries the RFC 9457 problem of a 404 (slug not-found)", async () => {
    mockApi({
      "GET /journeys/RN0000000000000000": problem(
        404, "not-found", "Resource not found", "not found: journey RN0000000000000000",
      ),
    });
    await expect(getJourney("RN0000000000000000")).rejects.toMatchObject({
      status: 404,
      slug: "not-found",
      title: "Resource not found",
      detail: "not found: journey RN0000000000000000",
    });
  });

  it("normalizes a network failure into an ApiError", async () => {
    mockApi({});
    await expect(getJourney("RN2")).rejects.toMatchObject({ status: 0, slug: "" });
  });
});
