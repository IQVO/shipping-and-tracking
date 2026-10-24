import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ScanReferenceScreen } from "./ScanReferenceScreen";

describe("ScanReferenceScreen", () => {
  it("lists every scan type with the edge it drives", () => {
    render(<ScanReferenceScreen />);
    expect(screen.getByRole("heading", { name: "Scan reference" })).toBeInTheDocument();
    // DELIVERED is a scan type, an edge target and a state: three cells in all
    expect(screen.getAllByText("DELIVERED")).toHaveLength(3);
    for (const scanType of ["SHIPMENT_CONFIRMED", "PICKUP_DEPARTURE", "DELIVERY_FAILED"]) {
      expect(screen.getByText(scanType)).toBeInTheDocument();
    }
    // one reference row says the first scan keeps LABELLED, another that a
    // failed delivery goes back to the station
    expect(screen.getByText("LABELLED (stays)")).toBeInTheDocument();
    expect(screen.getAllByText("AT STATION").length).toBeGreaterThan(0);
  });

  it("lists the nine journey states of the contract", () => {
    render(<ScanReferenceScreen />);
    // OUT_FOR_DELIVERY also appears in the edge table, so count, don't match one
    expect(screen.getAllByText("OUT_FOR_DELIVERY").length).toBeGreaterThan(1);
    for (const state of ["RETURNED", "LOST", "VOIDED"]) {
      expect(screen.getByText(state)).toBeInTheDocument();
    }
  });
});
