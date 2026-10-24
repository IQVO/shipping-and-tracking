/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

/** Label of Journey.state: where the package stands. */
export const JOURNEY_STATES = [
  "LABELLED",
  "HANDED_OVER",
  "IN_TRANSIT",
  "AT_STATION",
  "OUT_FOR_DELIVERY",
  "DELIVERED",
  "RETURNED",
  "LOST",
  "VOIDED",
] as const;
export type JourneyState = (typeof JOURNEY_STATES)[number];

/** Kind of observation (ADR 0002 A5); each type has fixed edges. */
export const SCAN_TYPES = [
  "SHIPMENT_CONFIRMED",
  "PICKUP_DEPARTURE",
  "DEPARTURE",
  "TRANSIT_ARRIVAL",
  "STATION_ARRIVAL",
  "OUT_FOR_DELIVERY",
  "DELIVERED",
  "DELIVERY_FAILED",
] as const;
export type ScanType = (typeof SCAN_TYPES)[number];

export type ServiceTier = "SAME_DAY" | "NEXT_DAY" | "TWO_DAY" | "STANDARD";

export interface Scan {
  scanId: string;
  nodeId: string;
  scanType: ScanType;
  at: string;
  /** False for a scan stored out of order or without an edge from the state it met. */
  applied: boolean;
}

/** GET /journeys/{trackingNumber} — the customer-visible timeline. */
export interface Journey {
  trackingNumber: string;
  state: JourneyState;
  poNumber: string;
  assignmentId: string;
  orderId: string;
  tier: ServiceTier;
  handedOverAt?: string;
  deliveredAt?: string;
  scans: Scan[];
}

/**
 * The scan-type reference of the contract (ADR 0002 A5): what each scan type
 * means and which state transition it drives. Static, shipped with the
 * remote; it can only change with apis/openapi.yaml.
 */
export const SCAN_EDGES: ReadonlyArray<{ type: ScanType; from: string; to: string }> = [
  { type: "SHIPMENT_CONFIRMED", from: "LABELLED", to: "LABELLED (stays)" },
  { type: "PICKUP_DEPARTURE", from: "LABELLED", to: "HANDED_OVER" },
  { type: "DEPARTURE", from: "HANDED_OVER or IN_TRANSIT", to: "IN_TRANSIT" },
  { type: "TRANSIT_ARRIVAL", from: "HANDED_OVER or IN_TRANSIT", to: "IN_TRANSIT" },
  { type: "STATION_ARRIVAL", from: "HANDED_OVER or IN_TRANSIT", to: "AT_STATION" },
  { type: "OUT_FOR_DELIVERY", from: "AT_STATION", to: "OUT_FOR_DELIVERY" },
  { type: "DELIVERED", from: "OUT_FOR_DELIVERY", to: "DELIVERED" },
  { type: "DELIVERY_FAILED", from: "OUT_FOR_DELIVERY", to: "AT_STATION" },
];
