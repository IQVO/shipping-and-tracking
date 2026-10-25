import { MemoryRouter, Route, Routes } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import App from "./App";
import { json, mockApi } from "./test/fetchMock";
import { journey } from "./test/fixtures";

// The console mounts the remote under its own splat route; the remote's
// relative links must resolve against the mount point, not the full URL.
function renderUnderHost(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/shipping-and-tracking/*" element={<App />} />
      </Routes>
    </MemoryRouter>,
  );
}

const ROUTES = {
  "GET /journeys/RN0123456789ABCDEF": json(journey()),
};

describe("App", () => {
  it("redirects the mount point to the journey lookup", async () => {
    mockApi(ROUTES);
    renderUnderHost("/shipping-and-tracking");
    expect(await screen.findByRole("heading", { name: "Package journeys" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Journeys" })).toHaveAttribute(
      "href", "/shipping-and-tracking/journeys",
    );
  });

  it("keeps the sub-nav at the mount point from a deep route", async () => {
    mockApi(ROUTES);
    renderUnderHost("/shipping-and-tracking/journeys/RN0123456789ABCDEF");
    await screen.findByRole("heading", { name: "Journey RN0123456789ABCDEF" });
    expect(screen.getByRole("link", { name: "Scan reference" })).toHaveAttribute(
      "href", "/shipping-and-tracking/scan-reference",
    );
  });

  it("navigates from the lookup into the journey and on to the scan reference", async () => {
    mockApi(ROUTES);
    renderUnderHost("/shipping-and-tracking/journeys");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Tracking number"), "RN0123456789ABCDEF");
    await user.click(screen.getByRole("button", { name: "View journey" }));
    expect(await screen.findByRole("heading", { name: "Journey RN0123456789ABCDEF" })).toBeInTheDocument();
    await user.click(screen.getByRole("link", { name: "Scan reference" }));
    expect(await screen.findByRole("heading", { name: "Scan reference" })).toBeInTheDocument();
  });
});
