import { Navigate, NavLink, Outlet, Route, Routes } from "react-router-dom";
import { JourneyLookupScreen } from "./screens/JourneyLookupScreen";
import { JourneyRoute } from "./screens/JourneyScreen";
import { ScanReferenceScreen } from "./screens/ScanReferenceScreen";

const SUB_NAV = [
  { to: "journeys", label: "Journeys" },
  { to: "scan-reference", label: "Scan reference" },
];

const linkStyle = ({ isActive }: { isActive: boolean }) => ({
  display: "inline-flex",
  padding: "6px 12px",
  borderRadius: "var(--wh-radius-pill)",
  fontSize: "var(--wh-font-size-sm)",
  fontWeight: isActive ? 600 : 500,
  color: isActive ? "var(--wh-color-text)" : "var(--wh-color-text-muted)",
  background: isActive ? "var(--wh-color-accent-muted)" : "transparent",
  textDecoration: "none",
});

/** The sub-nav, rendered by a layout route with the path "/".
 *
 *  The console mounts this component inside its own `<Route
 *  path="/shipping-and-tracking/*">`. A relative `<NavLink>` rendered straight
 *  in that splat route resolves against the FULL current URL (react-router 7
 *  uses the last path-contributing match's pathname, splat included), so from
 *  /shipping-and-tracking/journeys the link `scan-reference` would point at
 *  /shipping-and-tracking/journeys/scan-reference. A pathless layout route
 *  does not help (react-router drops pathless routes from relative
 *  resolution); a layout route WITH path "/" does: its own match is the mount
 *  point, under any prefix and standalone at `/`. (Same fix as the other
 *  fleet remotes; App.test.tsx mounts under the host's splat route to prove
 *  it.) */
function ShippingLayout() {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--wh-space-5)" }}>
      <nav aria-label="Shipping and tracking sections" style={{ display: "flex", gap: "var(--wh-space-2)" }}>
        {SUB_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} style={linkStyle}>
            {item.label}
          </NavLink>
        ))}
      </nav>
      <Outlet />
    </div>
  );
}

/** Exposed as shipping_mfe/App via Module Federation. Takes NO props: the
 *  console mounts it under `/shipping-and-tracking/*` and provides the
 *  BrowserRouter, the design tokens and `window.__WAREHOUSE_CONFIG__`. Routes
 *  here are RELATIVE, so the component works identically under that prefix
 *  (in the shell) or at / (standalone dev, see main.tsx). It calls ONLY this
 *  context's own API (/api/shipping-and-tracking), and only its GETs: this
 *  remote is read-only — issuing labels, voiding them and recording scans
 *  stay with vendor-connectivity and the API.
 *
 *   - (index): redirects to journeys.
 *   - journeys: a lookup by tracking number; journeys/:trackingNumber: the
 *     journey — its state, its label's facts and every stored scan.
 *   - scan-reference: the scan types and the edges they drive. */
export default function App() {
  return (
    <Routes>
      <Route path="/" element={<ShippingLayout />}>
        <Route index element={<Navigate to="journeys" replace />} />
        <Route path="journeys" element={<JourneyLookupScreen />} />
        <Route path="journeys/:trackingNumber" element={<JourneyRoute />} />
        <Route path="scan-reference" element={<ScanReferenceScreen />} />
      </Route>
    </Routes>
  );
}
