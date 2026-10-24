import type { ReactNode } from "react";
import { DataTable, StatusPill } from "@warehouse/ui-kit";
import { utcLabel } from "../lib";
import type { JourneyState, Scan } from "../types";

export function PageHeader({ title, subtitle }: { title: string; subtitle: ReactNode }) {
  return (
    <div>
      <h1 style={{ fontSize: "var(--wh-font-size-2xl)", margin: 0 }}>{title}</h1>
      <p style={{ color: "var(--wh-color-text-muted)", marginTop: 4 }}>{subtitle}</p>
    </div>
  );
}

export function Stack({ children, gap = 4 }: { children: ReactNode; gap?: 3 | 4 | 5 }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: `var(--wh-space-${gap})` }}>
      {children}
    </div>
  );
}

/** A label/value list (dl) for a record's scalar fields. */
export function Facts({ items }: { items: { label: string; value: ReactNode }[] }) {
  return (
    <dl
      style={{
        display: "grid",
        gridTemplateColumns: "max-content 1fr",
        gap: "var(--wh-space-2) var(--wh-space-4)",
        margin: 0,
        fontSize: "var(--wh-font-size-sm)",
      }}
    >
      {items.map((item) => (
        <div key={item.label} style={{ display: "contents" }}>
          <dt style={{ color: "var(--wh-color-text-muted)" }}>{item.label}</dt>
          <dd style={{ margin: 0 }}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

type Tone = "neutral" | "progress" | "success" | "warning" | "danger";

/** A journey's lifecycle is shipping-and-tracking's own, so StatusPill's tone
 *  override carries it. */
const STATE_TONE: Record<JourneyState, Tone> = {
  LABELLED: "neutral",
  HANDED_OVER: "progress",
  IN_TRANSIT: "progress",
  AT_STATION: "progress",
  OUT_FOR_DELIVERY: "progress",
  DELIVERED: "success",
  RETURNED: "warning",
  LOST: "danger",
  VOIDED: "neutral",
};

export function StatePill({ state }: { state: JourneyState }) {
  return <StatusPill status={state} tone={STATE_TONE[state]} size="sm" />;
}

/** Every stored scan of a journey, in the order they were recorded. */
export function ScanTable({ scans }: { scans: Scan[] }) {
  return (
    <DataTable
      rowKey={(s) => s.scanId}
      rows={scans}
      columns={[
        { key: "at", header: "Seen at", render: (s) => utcLabel(s.at) },
        { key: "type", header: "Scan", render: (s) => s.scanType },
        { key: "node", header: "Node", render: (s) => s.nodeId },
        { key: "applied", header: "Applied", render: (s) => (s.applied ? "yes" : "no — stored only") },
      ]}
      emptyState="No scan recorded yet"
    />
  );
}
