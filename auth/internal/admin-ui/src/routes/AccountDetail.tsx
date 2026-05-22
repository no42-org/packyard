import { useState } from "react";
import { Link, useParams } from "react-router-dom";

import {
  Key,
  useAccount,
  useAccountKeys,
  useIssueAccountKey,
  useRevokeKey,
} from "../api/accounts";
import { useComponents } from "../api/components";
import { ErrorBanner } from "../components/ErrorBanner";
import { Modal } from "../components/Modal";
import { Pagination } from "../components/Pagination";

export function AccountDetail() {
  const { id = "" } = useParams<{ id: string }>();
  const acct = useAccount(id);
  const keys = useAccountKeys(id, {});
  const [issueOpen, setIssueOpen] = useState(false);
  const [issuedSecret, setIssuedSecret] = useState<string | null>(null);

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">
          <Link to="/accounts">Accounts</Link> /{" "}
          {acct.data ? acct.data.email : <span className="muted">…</span>}
        </h1>
        <button onClick={() => setIssueOpen(true)}>+ Issue key</button>
      </div>

      <ErrorBanner error={acct.error} />

      {acct.data && (
        <div className="filters">
          <div>
            <strong>Org:</strong> {acct.data.org_name}
          </div>
          <div>
            <strong>Status:</strong>{" "}
            <span className={`status-pill status-${acct.data.status}`}>{acct.data.status}</span>
          </div>
          <div>
            <strong>Created:</strong> {new Date(acct.data.created_at).toLocaleString()}
          </div>
        </div>
      )}

      <h2>Keys</h2>
      <ErrorBanner error={keys.error} />

      {keys.isLoading ? (
        <div className="muted">Loading…</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Component</th>
              <th>Label</th>
              <th>Active</th>
              <th>Created</th>
              <th>Expires</th>
              <th>Usage</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(keys.data?.items ?? []).map((k) => (
              <KeyRow key={k.id} k={k} />
            ))}
            {(keys.data?.items ?? []).length === 0 && (
              <tr>
                <td colSpan={7} className="muted">
                  No keys issued yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}

      <Pagination links={keys.data?.links ?? {}} />

      <IssueKeyModal
        accountId={id}
        open={issueOpen}
        onClose={() => setIssueOpen(false)}
        onIssued={(secret) => {
          setIssuedSecret(secret);
          setIssueOpen(false);
        }}
      />

      <IssuedSecretModal secret={issuedSecret} onClose={() => setIssuedSecret(null)} />
    </div>
  );
}

function KeyRow({ k }: { k: Key }) {
  const revoke = useRevokeKey();
  const [confirming, setConfirming] = useState(false);

  const submit = async () => {
    try {
      await revoke.mutateAsync(k.id);
      setConfirming(false);
    } catch {
      /* surfaced inside modal */
    }
  };

  return (
    <tr>
      <td>{k.component}</td>
      <td>{k.label || <span className="muted">—</span>}</td>
      <td>
        <span className={`status-pill status-${k.active ? "active" : "deleted"}`}>
          {k.active ? "active" : "revoked"}
        </span>
      </td>
      <td>{new Date(k.created_at).toLocaleDateString()}</td>
      <td>{k.expires_at ? new Date(k.expires_at).toLocaleDateString() : <span className="muted">—</span>}</td>
      <td>{k.usage_count}</td>
      <td>
        {k.active && (
          <button
            className="btn-danger"
            disabled={revoke.isPending || confirming}
            onClick={() => setConfirming(true)}
          >
            Revoke
          </button>
        )}
        {confirming && (
          <Modal
            open={true}
            title="Revoke subscription key"
            onClose={() => setConfirming(false)}
            actions={
              <>
                <button className="btn-secondary" onClick={() => setConfirming(false)} disabled={revoke.isPending}>
                  Cancel
                </button>
                <button className="btn-danger" onClick={submit} disabled={revoke.isPending}>
                  Revoke
                </button>
              </>
            }
          >
            <p>
              Revoke key <code>{k.id}</code>? Any subscriber currently using it will be rejected on the next request.
              Revocation is immediate and irreversible.
            </p>
            <ErrorBanner error={revoke.error} />
          </Modal>
        )}
      </td>
    </tr>
  );
}

function IssueKeyModal({
  accountId,
  open,
  onClose,
  onIssued,
}: {
  accountId: string;
  open: boolean;
  onClose: () => void;
  onIssued: (secret: string) => void;
}) {
  const components = useComponents();
  const issue = useIssueAccountKey(accountId);
  const [component, setComponent] = useState("");
  const [label, setLabel] = useState("");
  const [expires, setExpires] = useState("");

  const expiresError = expires && !isValidRFC3339(expires)
    ? "Use RFC 3339 (e.g. 2027-01-01T00:00:00Z), or leave empty."
    : null;

  const submit = async () => {
    if (expiresError) return;
    try {
      const res = await issue.mutateAsync({
        component,
        label: label || undefined,
        expires_at: expires || null,
      });
      // The key id IS the subscriber secret — there is no separate secret
      // field. Backend `wrapKey` returns the flat key record; SPA reveals
      // `id` as the one-time copy-out value.
      onIssued(res.id);
      setComponent("");
      setLabel("");
      setExpires("");
    } catch {
      /* error rendered below */
    }
  };

  return (
    <Modal
      open={open}
      title="Issue subscription key"
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button onClick={submit} disabled={issue.isPending || !component}>
            Issue
          </button>
        </>
      }
    >
      <div className="field">
        <label>Component</label>
        <select value={component} onChange={(e) => setComponent(e.target.value)}>
          <option value="">— select —</option>
          {(components.data ?? []).map((c) => (
            <option key={c.name} value={c.name}>
              {c.name}
            </option>
          ))}
        </select>
      </div>
      <div className="field">
        <label>Label (optional)</label>
        <input type="text" value={label} onChange={(e) => setLabel(e.target.value)} />
      </div>
      <div className="field">
        <label>Expires (optional, RFC 3339)</label>
        <input
          type="text"
          placeholder="2027-01-01T00:00:00Z"
          value={expires}
          onChange={(e) => setExpires(e.target.value)}
        />
        {expiresError && (
          <div className="error-banner" role="alert">{expiresError}</div>
        )}
      </div>
      <ErrorBanner error={components.error || issue.error} />
    </Modal>
  );
}

// isValidRFC3339 checks that a string parses as a Date AND, when reformatted
// via toISOString(), produces a value that round-trips. The backend's
// `*time.Time` JSON unmarshal uses `time.Parse(time.RFC3339, …)`, which
// requires the explicit time-of-day and zone designator — Date-only inputs
// like "2027-01-01" parse via JavaScript's Date constructor but get
// rejected server-side. This guard surfaces the typo before the request.
function isValidRFC3339(s: string): boolean {
  // Require a 'T' separator and a zone designator (Z or ±hh:mm).
  if (!/T/.test(s) || !/(Z|[+-]\d{2}:?\d{2})$/.test(s)) return false;
  const t = Date.parse(s);
  return !Number.isNaN(t);
}

function IssuedSecretModal({ secret, onClose }: { secret: string | null; onClose: () => void }) {
  if (!secret) return null;
  return (
    <Modal
      open={true}
      title="Key issued"
      onClose={onClose}
      actions={
        <button className="btn-secondary" onClick={onClose}>
          Done
        </button>
      }
    >
      <p>
        Copy this secret now. It will not be shown again. The user must paste it into their package
        manager configuration verbatim.
      </p>
      <div className="field">
        <label>Secret</label>
        <textarea readOnly rows={3} value={secret} onFocus={(e) => e.target.select()} />
      </div>
    </Modal>
  );
}
