import { SHIPPING_API_BASE } from "./config";
import type { Journey } from "./types";

/** RFC 9457 problem+json body every error response from shipping-and-tracking
 *  returns (`type` = https://errors.shipping-and-tracking.warehouse-systems.dev/<slug>). */
export interface ProblemDetails {
  type?: string;
  title?: string;
  status?: number;
  detail?: string;
  instance?: string;
}

/**
 * Every failed call -- an HTTP error with a problem+json body, an HTTP error
 * without one, or a network failure -- surfaces as one ApiError, so a screen
 * has a single shape to render: `title` (the problem's category) and `detail`
 * (what exactly was wrong), plus `slug` to branch on a specific problem
 * (e.g. `not-found`).
 */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: ProblemDetails | null;
  readonly title: string;
  readonly detail: string;
  /** Last path segment of problem.type (e.g. "not-found"); "" when unknown. */
  readonly slug: string;

  constructor(status: number, problem: ProblemDetails | null, fallbackTitle: string) {
    const title = problem?.title || fallbackTitle;
    const detail = problem?.detail ?? "";
    super(detail || title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
    this.title = title;
    this.detail = detail;
    this.slug = problem?.type ? (problem.type.split("/").pop() ?? "") : "";
  }
}

/** Normalizes anything thrown by a request into an ApiError. */
export function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  const message = err instanceof Error ? err.message : String(err);
  return new ApiError(0, { detail: message }, "Network error");
}

/**
 * The one door to the service. Only this context's own API is ever called
 * (`SHIPPING_API_BASE` = <apiOrigin>/api/shipping-and-tracking): no sibling
 * context, no reports service. Every call here is a GET with no custom
 * header, so they stay simple CORS requests.
 */
async function request<T>(path: string): Promise<T> {
  let res: Response;
  try {
    res = await fetch(`${SHIPPING_API_BASE}${path}`);
  } catch (err) {
    throw toApiError(err);
  }
  if (!res.ok) {
    let problem: ProblemDetails | null = null;
    try {
      problem = (await res.json()) as ProblemDetails;
    } catch {
      // non-JSON error body -- fall through with problem = null
    }
    throw new ApiError(res.status, problem, `${res.status} ${res.statusText}`.trim());
  }
  return (await res.json()) as T;
}

const seg = encodeURIComponent;

/** A journey as the UI uses it: the scan list is never undefined. */
function normalizeJourney(j: Journey): Journey {
  return { ...j, scans: j.scans ?? [] };
}

/** GET /journeys/{trackingNumber} (404 not-found). */
export async function getJourney(trackingNumber: string): Promise<Journey> {
  return normalizeJourney(await request<Journey>(`/journeys/${seg(trackingNumber)}`));
}
