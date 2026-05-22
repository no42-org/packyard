import { useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import {
  Account,
  AccountStatus,
  useAccounts,
  useCreateAccount,
  useDeleteAccount,
  useUpdateAccount,
} from "../api/accounts";
import { ErrorBanner } from "../components/ErrorBanner";
import { Modal } from "../components/Modal";
import { Pagination } from "../components/Pagination";

export function Accounts() {
  const [params, setParams] = useSearchParams();
  const status = (params.get("status") || undefined) as AccountStatus | undefined;
  const search = params.get("q") || "";
  const offset = Number(params.get("offset") || 0);
  const limit = Number(params.get("limit") || 50);

  const { data, error, isLoading } = useAccounts({ status, offset, limit });
  const [createOpen, setCreateOpen] = useState(false);

  const items = data?.items ?? [];
  const filtered = search
    ? items.filter(
        (a) =>
          a.email.toLowerCase().includes(search.toLowerCase()) ||
          a.org_name.toLowerCase().includes(search.toLowerCase()),
      )
    : items;

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Accounts</h1>
        <button onClick={() => setCreateOpen(true)}>+ New account</button>
      </div>

      <div className="filters">
        <input
          type="search"
          placeholder="Search email / org"
          value={search}
          onChange={(e) => updateParam(params, setParams, "q", e.target.value || null)}
        />
        <select
          value={status ?? ""}
          onChange={(e) => updateParam(params, setParams, "status", e.target.value || null)}
        >
          <option value="">All statuses</option>
          <option value="active">active</option>
          <option value="suspended">suspended</option>
          <option value="deleted">deleted</option>
        </select>
      </div>

      <ErrorBanner error={error} />

      {isLoading ? (
        <div className="muted">Loading…</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Email</th>
              <th>Org</th>
              <th>Status</th>
              <th>Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((a) => (
              <AccountRow key={a.id} a={a} />
            ))}
            {filtered.length === 0 && (
              <tr>
                <td colSpan={5} className="muted">
                  No accounts match.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}

      <Pagination links={data?.links ?? {}} />

      <CreateAccountModal open={createOpen} onClose={() => setCreateOpen(false)} />
    </div>
  );
}

function AccountRow({ a }: { a: Account }) {
  const update = useUpdateAccount(a.id);
  const [deleting, setDeleting] = useState(false);

  return (
    <tr>
      <td>
        <Link to={`/accounts/${a.id}`}>{a.email}</Link>
      </td>
      <td>{a.org_name}</td>
      <td>
        <span className={`status-pill status-${a.status}`}>{a.status}</span>
      </td>
      <td>{new Date(a.created_at).toLocaleDateString()}</td>
      <td>
        {a.status === "active" && (
          <button
            className="btn-secondary"
            onClick={() => update.mutate({ status: "suspended" })}
            disabled={update.isPending}
          >
            Suspend
          </button>
        )}
        {a.status === "suspended" && (
          <button
            className="btn-secondary"
            onClick={() => update.mutate({ status: "active" })}
            disabled={update.isPending}
          >
            Reactivate
          </button>
        )}
        {a.status !== "deleted" && (
          <button className="btn-danger" onClick={() => setDeleting(true)}>
            Delete
          </button>
        )}
        {deleting && <DeleteAccountModal account={a} onClose={() => setDeleting(false)} />}
      </td>
    </tr>
  );
}

function CreateAccountModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const create = useCreateAccount();
  const [email, setEmail] = useState("");
  const [org, setOrg] = useState("");

  const submit = async () => {
    try {
      await create.mutateAsync({ email, org_name: org });
      setEmail("");
      setOrg("");
      onClose();
    } catch {
      /* error rendered below */
    }
  };

  return (
    <Modal
      open={open}
      title="New account"
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button onClick={submit} disabled={create.isPending || !email || !org}>
            Create
          </button>
        </>
      }
    >
      <div className="field">
        <label>Email</label>
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="field">
        <label>Organisation</label>
        <input type="text" value={org} onChange={(e) => setOrg(e.target.value)} />
      </div>
      <ErrorBanner error={create.error} />
    </Modal>
  );
}

function DeleteAccountModal({ account, onClose }: { account: Account; onClose: () => void }) {
  const del = useDeleteAccount();
  // D24 confirm-name pattern: operator must type the account id to confirm.
  const [confirm, setConfirm] = useState("");
  // Synchronous double-submit guard. React Query's `isPending` flips a tick
  // after the click, so two fast clicks can both reach `mutateAsync` before
  // the button rerenders as disabled. A ref-backed flag closes that window.
  const inflight = useRef(false);

  const submit = async () => {
    if (inflight.current) return;
    inflight.current = true;
    try {
      await del.mutateAsync({ id: account.id, confirm: account.id });
      onClose();
    } catch {
      /* error rendered below */
    } finally {
      inflight.current = false;
    }
  };

  return (
    <Modal
      open={true}
      title="Delete account"
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button
            className="btn-danger"
            onClick={submit}
            disabled={del.isPending || confirm !== account.id}
          >
            Delete &amp; revoke keys
          </button>
        </>
      }
    >
      <p>
        Deleting <strong>{account.email}</strong> will revoke every active key it owns. This cannot
        be undone.
      </p>
      <div className="field">
        <label>Type the account id to confirm: <code>{account.id}</code></label>
        <input type="text" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
      </div>
      <ErrorBanner error={del.error} />
    </Modal>
  );
}

function updateParam(
  params: URLSearchParams,
  setParams: (p: URLSearchParams) => void,
  key: string,
  value: string | null,
) {
  const next = new URLSearchParams(params);
  if (value === null || value === "") next.delete(key);
  else next.set(key, value);
  next.delete("offset"); // reset pagination on filter change
  setParams(next);
}
