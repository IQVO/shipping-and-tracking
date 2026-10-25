import { Card, DataTable } from "@warehouse/ui-kit";
import { PageHeader, Stack } from "../components/layout";
import { JOURNEY_STATES, SCAN_EDGES } from "../types";

const stateCell = (code: string): string => code.replace(/_/g, " ");

/**
 * The scan reference of the contract (ADR 0002 A5): every scan type and the
 * edge it drives, plus the nine journey states. Static — it changes only with
 * apis/openapi.yaml — so operators reading a journey can tell an applied scan
 * from a stored-only one without leaving the console.
 */
export function ScanReferenceScreen() {
  return (
    <Stack gap={5}>
      <PageHeader
        title="Scan reference"
        subtitle="What each scan means and which state it drives (fixed by the contract)"
      />
      <Card title="Scan types and their edges">
        <DataTable
          rowKey={(e) => e.type}
          rows={[...SCAN_EDGES]}
          columns={[
            { key: "type", header: "Scan type", render: (e) => e.type },
            { key: "from", header: "From state", render: (e) => stateCell(e.from) },
            { key: "to", header: "To state", render: (e) => stateCell(e.to) },
          ]}
        />
      </Card>
      <Card title="Journey states">
        <ul style={{ margin: 0, paddingLeft: 20, fontSize: "var(--wh-font-size-sm)" }}>
          {JOURNEY_STATES.map((s) => (
            <li key={s} style={{ fontFamily: "var(--wh-font-mono)" }}>
              {s}
            </li>
          ))}
        </ul>
      </Card>
    </Stack>
  );
}
