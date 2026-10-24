import { Link, useParams } from "react-router-dom";
import { Card } from "@warehouse/ui-kit";
import { getJourney } from "../api";
import { RequestView } from "../components/ProblemAlert";
import { Facts, PageHeader, ScanTable, Stack, StatePill } from "../components/layout";
import { useRequest } from "../hooks/useRequest";
import { utcLabel } from "../lib";
import type { Journey } from "../types";

/** The route element of `journeys/:trackingNumber`. */
export function JourneyRoute() {
  const { trackingNumber } = useParams();
  return trackingNumber ? <JourneyScreen trackingNumber={trackingNumber} /> : null;
}

/**
 * One journey: GET /journeys/{trackingNumber} — the state, the label's facts
 * (never an address: the contract keeps the ship-to out of every response)
 * and every stored scan, applied or not, in the order they were recorded. An
 * unknown tracking number is a 404 not-found problem.
 */
export function JourneyScreen({ trackingNumber }: { trackingNumber: string }) {
  const journey = useRequest(() => getJourney(trackingNumber), `journey|${trackingNumber}`);
  return (
    <Stack gap={5}>
      <PageHeader
        title={`Journey ${trackingNumber}`}
        subtitle="The customer-visible timeline of this package"
      />
      {/* The empty case is carried by ScanTable's own emptyState. */}
      <RequestView state={journey} what="the journey">
        {(j) => <JourneyBody journey={j} />}
      </RequestView>
      <Link to="../journeys">← Look up another journey</Link>
    </Stack>
  );
}

function JourneyBody({ journey }: { journey: Journey }) {
  return (
    <>
      <Card title="Journey">
        <Facts
          items={[
            { label: "Tracking number", value: journey.trackingNumber },
            { label: "State", value: <StatePill state={journey.state} /> },
            { label: "Purchase order", value: journey.poNumber },
            { label: "Assignment", value: journey.assignmentId },
            { label: "Order", value: journey.orderId },
            { label: "Service tier", value: journey.tier },
            { label: "Handed over", value: utcLabel(journey.handedOverAt) },
            { label: "Delivered", value: utcLabel(journey.deliveredAt) },
          ]}
        />
      </Card>
      <Card title={`Scans (${journey.scans.length})`}>
        <ScanTable scans={journey.scans} />
      </Card>
    </>
  );
}
