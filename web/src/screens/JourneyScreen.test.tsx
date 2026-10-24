import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { JourneyRoute } from "./JourneyScreen";
import { json, mockApi, problem } from "../test/fetchMock";
import { journey } from "../test/fixtures";

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/journeys/:trackingNumber" element={<JourneyRoute />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("JourneyScreen", () => {
  it("shows the state, the label facts and every scan with its applied flag", async () => {
    mockApi({ "GET /journeys/RN0123456789ABCDEF": json(journey()) });
    renderAt("/journeys/RN0123456789ABCDEF");
    expect(await screen.findByRole("heading", { name: "Journey RN0123456789ABCDEF" })).toBeInTheDocument();
    expect(screen.getByText("IN_TRANSIT")).toBeInTheDocument();
    expect(screen.getByText("PO-1")).toBeInTheDocument();
    expect(screen.getByText("asg-1")).toBeInTheDocument();
    expect(screen.getByText("ord-1")).toBeInTheDocument();
    expect(screen.getByText("NEXT_DAY")).toBeInTheDocument();
    expect(screen.getByText("2026-10-24 10:00:00Z")).toBeInTheDocument();
    // every scan, in the order they were recorded
    expect(screen.getByRole("columnheader", { name: "Seen at" })).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(1 + 3);
    expect(screen.getByText("PICKUP_DEPARTURE")).toBeInTheDocument();
    expect(screen.getByText("TRANSIT_ARRIVAL")).toBeInTheDocument();
    // the stored-only scan is marked, the applied ones are not
    expect(screen.getByText("no — stored only")).toBeInTheDocument();
    expect(screen.getAllByText("yes")).toHaveLength(2);
    // the hand-over fact and the pickup-departure row name the same instant
    expect(screen.getAllByText("2026-10-24 09:00:00Z")).toHaveLength(2);
  });

  it("says when a journey has no scan yet", async () => {
    mockApi({
      "GET /journeys/RN1": json(
        journey({ trackingNumber: "RN1", state: "LABELLED", handedOverAt: undefined, scans: [] }),
      ),
    });
    renderAt("/journeys/RN1");
    expect(await screen.findByText("No scan recorded yet")).toBeInTheDocument();
    expect(screen.getByText("LABELLED")).toBeInTheDocument();
  });

  it("explains an unknown tracking number as the not-found problem", async () => {
    mockApi({
      "GET /journeys/RN0000000000000000": problem(
        404, "not-found", "Resource not found", "not found: journey RN0000000000000000",
      ),
    });
    renderAt("/journeys/RN0000000000000000");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Resource not found");
    expect(alert).toHaveTextContent("not found: journey RN0000000000000000");
  });

  it("shows the delivered instant when the journey closed at the doorstep", async () => {
    mockApi({
      "GET /journeys/RN2": json(
        journey({ trackingNumber: "RN2", state: "DELIVERED", deliveredAt: "2026-10-25T17:30:00Z" }),
      ),
    });
    renderAt("/journeys/RN2");
    expect(await screen.findByText("2026-10-25 17:30:00Z")).toBeInTheDocument();
  });
});
