import { useState } from "react";

import {
  Operator,
  OperatorRole,
  OperatorStatus,
  useAddOperator,
  useOperators,
  useUpdateOperator,
} from "../api/operators";
import { ErrorBanner } from "../components/ErrorBanner";
import { Modal } from "../components/Modal";
import { Pagination } from "../components/Pagination";

export function Operators() {
  const list = useOperators({});
  const [addOpen, setAddOpen] = useState(false);

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Operators</h1>
        <button onClick={() => setAddOpen(true)}>+ Allowlist operator</button>
      </div>

      <ErrorBanner error={list.error} />

      {list.isLoading ? (
        <div className="muted">Loading…</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Email</th>
              <th>Role</th>
              <th>Status</th>
              <th>Last login</th>
              <th>Provider</th>
              <th>Allowlisted</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(list.data?.items ?? []).map((op) => (
              <OperatorRow key={op.id} op={op} />
            ))}
            {(list.data?.items ?? []).length === 0 && (
              <tr>
                <td colSpan={7} className="muted">
                  No operators allowlisted yet.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}

      <Pagination links={list.data?.links ?? {}} />

      <AddOperatorModal open={addOpen} onClose={() => setAddOpen(false)} />
    </div>
  );
}

function OperatorRow({ op }: { op: Operator }) {
  const update = useUpdateOperator(op.id);
  // Inline operator mutations gate behind an explicit confirm so a misclick
  // on a peer's role or status doesn't take effect immediately. The
  // self-lockout guard on the server catches the last-admin case, but a
  // peer-demote-by-mistake is otherwise silent.
  const [pending, setPending] = useState<
    | { kind: "role"; newRole: OperatorRole }
    | { kind: "status"; newStatus: OperatorStatus }
    | null
  >(null);

  const confirm = async () => {
    if (!pending) return;
    try {
      if (pending.kind === "role") {
        await update.mutateAsync({ role: pending.newRole });
      } else {
        await update.mutateAsync({ status: pending.newStatus });
      }
    } catch {
      /* error surfaced via update.error */
    }
    setPending(null);
  };

  return (
    <tr>
      <td>{op.email}</td>
      <td>
        <select
          value={op.role}
          onChange={(e) => {
            const newRole = e.target.value as OperatorRole;
            if (newRole !== op.role) setPending({ kind: "role", newRole });
          }}
          disabled={update.isPending || pending !== null}
        >
          <option value="admin">admin</option>
          <option value="readonly">readonly</option>
        </select>
      </td>
      <td>
        <span className={`status-pill status-${op.status}`}>{op.status}</span>
      </td>
      <td>{op.last_login_at ? new Date(op.last_login_at).toLocaleString() : <span className="muted">never</span>}</td>
      <td>
        {op.first_seen_provider ?? <span className="muted">—</span>}
        {op.github_username && <div className="muted">gh: {op.github_username}</div>}
        {op.microsoft_upn && <div className="muted">ms: {op.microsoft_upn}</div>}
      </td>
      <td>{new Date(op.allowlisted_at).toLocaleDateString()}</td>
      <td>
        {op.status === "active" ? (
          <button
            className="btn-secondary"
            disabled={update.isPending || pending !== null}
            onClick={() => setPending({ kind: "status", newStatus: "disabled" })}
          >
            Disable
          </button>
        ) : (
          <button
            className="btn-secondary"
            disabled={update.isPending || pending !== null}
            onClick={() => setPending({ kind: "status", newStatus: "active" })}
          >
            Re-enable
          </button>
        )}
      </td>
      {pending && (
        <td>
          <ConfirmOperatorChange
            op={op}
            pending={pending}
            onConfirm={confirm}
            onCancel={() => setPending(null)}
            error={update.error}
            inflight={update.isPending}
          />
        </td>
      )}
    </tr>
  );
}

function ConfirmOperatorChange({
  op,
  pending,
  onConfirm,
  onCancel,
  error,
  inflight,
}: {
  op: Operator;
  pending:
    | { kind: "role"; newRole: OperatorRole }
    | { kind: "status"; newStatus: OperatorStatus };
  onConfirm: () => void;
  onCancel: () => void;
  error: unknown;
  inflight: boolean;
}) {
  const title =
    pending.kind === "role"
      ? `Change role: ${op.email}`
      : pending.newStatus === "disabled"
        ? `Disable operator: ${op.email}`
        : `Re-enable operator: ${op.email}`;
  const body =
    pending.kind === "role"
      ? `Set role to ${pending.newRole}? Demoting an admin force-logs out their existing sessions.`
      : pending.newStatus === "disabled"
        ? `Disable this operator? Their sessions are deleted immediately and the next login is rejected with OPERATOR_DISABLED.`
        : `Re-enable this operator? They will be able to sign in on their next visit.`;
  return (
    <Modal
      open={true}
      title={title}
      onClose={onCancel}
      actions={
        <>
          <button className="btn-secondary" onClick={onCancel} disabled={inflight}>
            Cancel
          </button>
          <button
            className={pending.kind === "status" && pending.newStatus === "disabled" ? "btn-danger" : ""}
            onClick={onConfirm}
            disabled={inflight}
          >
            Confirm
          </button>
        </>
      }
    >
      <p>{body}</p>
      <ErrorBanner error={error} />
    </Modal>
  );
}

function AddOperatorModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const add = useAddOperator();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<OperatorRole>("admin");

  const submit = async () => {
    try {
      await add.mutateAsync({ email, role });
      setEmail("");
      setRole("admin");
      onClose();
    } catch {
      /* error rendered below */
    }
  };

  return (
    <Modal
      open={open}
      title="Allowlist operator"
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button onClick={submit} disabled={add.isPending || !email}>
            Add
          </button>
        </>
      }
    >
      <div className="field">
        <label>Email (must match the operator's OAuth identity)</label>
        <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="field">
        <label>Role</label>
        <select value={role} onChange={(e) => setRole(e.target.value as OperatorRole)}>
          <option value="admin">admin</option>
          <option value="readonly">readonly</option>
        </select>
      </div>
      <ErrorBanner error={add.error} />
    </Modal>
  );
}
