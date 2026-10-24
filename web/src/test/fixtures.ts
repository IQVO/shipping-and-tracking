import type { Journey, Scan } from "../types";

export const SCANS: Scan[] = [
  { scanId: "scan-1", nodeId: "node-A", scanType: "PICKUP_DEPARTURE", at: "2026-10-24T09:00:00Z", applied: true },
  { scanId: "scan-2", nodeId: "node-B", scanType: "DEPARTURE", at: "2026-10-24T10:00:00Z", applied: true },
  { scanId: "scan-3", nodeId: "node-C", scanType: "TRANSIT_ARRIVAL", at: "2026-10-24T08:30:00Z", applied: false },
];

export function journey(over: Partial<Journey> = {}): Journey {
  return {
    trackingNumber: "RN0123456789ABCDEF",
    state: "IN_TRANSIT",
    poNumber: "PO-1",
    assignmentId: "asg-1",
    orderId: "ord-1",
    tier: "NEXT_DAY",
    handedOverAt: "2026-10-24T09:00:00Z",
    scans: SCANS,
    ...over,
  };
}
