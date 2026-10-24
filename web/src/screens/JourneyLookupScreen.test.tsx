import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { JourneyLookupScreen } from "./JourneyLookupScreen";

function Where() {
  return <div data-testid="where">{useLocation().pathname}</div>;
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/journeys" element={<JourneyLookupScreen />} />
      </Routes>
      <Where />
    </MemoryRouter>,
  );
}

describe("JourneyLookupScreen", () => {
  it("opens the journey, keeping the tracking number as typed", async () => {
    renderAt("/journeys");
    const user = userEvent.setup();
    expect(screen.getByRole("button", { name: "View journey" })).toBeDisabled();
    await user.type(screen.getByLabelText("Tracking number"), "RN0123456789ABCDEF");
    await user.click(screen.getByRole("button", { name: "View journey" }));
    expect(screen.getByTestId("where")).toHaveTextContent("/journeys/RN0123456789ABCDEF");
  });

  it("refuses a malformed tracking number before asking", async () => {
    renderAt("/journeys");
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Tracking number"), "RN/1");
    expect(screen.getByRole("button", { name: "View journey" })).toBeDisabled();
    expect(screen.getByText(/cannot contain/)).toBeInTheDocument();
  });
});
