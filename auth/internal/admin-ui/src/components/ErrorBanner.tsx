import { ApiError } from "../api/client";

interface Props {
  error: unknown;
}

// ErrorBanner renders the structured error envelope returned by the admin
// API (`{code, message}`). Falls back to a generic message for non-ApiError
// failures (network drops, JSON parse errors, etc.) so the UI always tells
// the operator *something*.
export function ErrorBanner({ error }: Props) {
  if (!error) return null;
  if (error instanceof ApiError) {
    return (
      <div className="error-banner">
        <strong>{error.code}</strong> — {error.message}
      </div>
    );
  }
  const msg = error instanceof Error ? error.message : String(error);
  return <div className="error-banner">{msg}</div>;
}
