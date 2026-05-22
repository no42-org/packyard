import { useNavigate } from "react-router-dom";

interface Props {
  links: { prev?: string; next?: string };
}

// Pagination renders prev/next buttons driven by the RFC 5988 Link header
// returned from list endpoints (D23). The server-supplied URLs already
// contain the right `offset`/`limit` query, so we route to them as-is. URLs
// are normalised to relative paths so React Router handles them.
export function Pagination({ links }: Props) {
  const nav = useNavigate();
  return (
    <div className="pagination">
      <button
        type="button"
        className="btn-secondary"
        disabled={!links.prev}
        onClick={() => {
          if (!links.prev) return;
          const target = toRelative(links.prev);
          if (target) nav(target);
        }}
      >
        ← Prev
      </button>
      <button
        type="button"
        className="btn-secondary"
        disabled={!links.next}
        onClick={() => {
          if (!links.next) return;
          const target = toRelative(links.next);
          if (target) nav(target);
        }}
      >
        Next →
      </button>
    </div>
  );
}

// toRelative converts a Link-header URL into a React-Router-relative path
// (the BrowserRouter basename is /admin, so the leading "/admin" prefix is
// stripped). Server-controlled URLs are NOT trusted blindly:
//
//   - non-http(s) schemes (javascript:, data:, file:) are rejected
//   - cross-origin URLs are rejected (defence-in-depth in case a backend
//     bug ever leaks an external URL into a Link header)
//
// Failures fall back to "" — the Pagination button is already disabled
// when the link is absent; an invalid link is treated as no link.
function toRelative(url: string): string {
  try {
    const u = new URL(url, window.location.origin);
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      return "";
    }
    if (u.origin !== window.location.origin) {
      return "";
    }
    return u.pathname.replace(/^\/admin/, "") + u.search;
  } catch {
    return "";
  }
}
