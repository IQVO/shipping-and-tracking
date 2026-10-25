import { NO_VALUE } from "@warehouse/ui-kit";

/** "2026-10-09 08:05:30Z" for an RFC 3339 instant; the raw text when unparseable. */
export function utcLabel(iso: string | undefined): string {
  if (!iso) return NO_VALUE;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  const d = new Date(t).toISOString();
  return `${d.slice(0, 10)} ${d.slice(11, 19)}Z`;
}

/**
 * The tracking-number rule of the contract: a non-empty id of at most 64
 * characters with no whitespace, control characters or "/". The message is
 * worded for the field the user typed in.
 */
export function trackingNumberProblem(tn: string): string | null {
  const id = tn.trim();
  if (id.length === 0) return "Enter a tracking number.";
  if (id.length > 64) return "A tracking number is at most 64 characters.";
  // eslint-disable-next-line no-control-regex
  if (/[\s/\u0000-\u001f\u007f]/.test(id)) return "A tracking number cannot contain spaces, control characters or '/'.";
  return null;
}
