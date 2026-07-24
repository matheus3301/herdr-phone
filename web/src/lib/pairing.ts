/** Extract a pairing secret from either a raw secret or a full pairing URL. */
export function extractPairingSecret(input: string): string {
  const trimmed = input.trim();
  const match = trimmed.match(/pair=([^&\s]+)/);
  if (match) {
    try {
      return decodeURIComponent(match[1]);
    } catch {
      return match[1];
    }
  }
  return trimmed;
}
