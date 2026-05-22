import { useEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { AuditEntry, useAuditLog } from "../api/audit";
import { ErrorBanner } from "../components/ErrorBanner";
import { Pagination } from "../components/Pagination";

const FILTER_KEYS = ["operator", "action", "target_type", "target_id", "since", "until"] as const;
type FilterKey = (typeof FILTER_KEYS)[number];

export function Audit() {
  const [params, setParams] = useSearchParams();
  const offset = Number(params.get("offset") || 0);
  const limit = Number(params.get("limit") || 50);

  const filters = {
    operator: params.get("operator") || undefined,
    action: params.get("action") || undefined,
    target_type: params.get("target_type") || undefined,
    target_id: params.get("target_id") || undefined,
    since: params.get("since") || undefined,
    until: params.get("until") || undefined,
    offset,
    limit,
  };

  const list = useAuditLog(filters);

  // Controlled filter state with a 350ms debounce. Previously the inputs
  // used `defaultValue` + `onBlur`, which discarded the user's typed value
  // if they clicked Pagination before blurring — the input would unmount
  // without firing onBlur, taking the edit with it.
  const [draft, setDraft] = useState<Record<FilterKey, string>>(() => ({
    operator: params.get("operator") ?? "",
    action: params.get("action") ?? "",
    target_type: params.get("target_type") ?? "",
    target_id: params.get("target_id") ?? "",
    since: params.get("since") ?? "",
    until: params.get("until") ?? "",
  }));

  const debounceRef = useRef<number | undefined>(undefined);
  useEffect(() => {
    if (debounceRef.current !== undefined) window.clearTimeout(debounceRef.current);
    debounceRef.current = window.setTimeout(() => {
      const next = new URLSearchParams(params);
      let changed = false;
      for (const k of FILTER_KEYS) {
        const v = draft[k];
        const current = params.get(k) ?? "";
        if (v === current) continue;
        changed = true;
        if (v) next.set(k, v);
        else next.delete(k);
      }
      if (changed) {
        next.delete("offset");
        setParams(next);
      }
    }, 350);
    return () => {
      if (debounceRef.current !== undefined) window.clearTimeout(debounceRef.current);
    };
  }, [draft, params, setParams]);

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Audit log</h1>
      </div>

      <div className="filters">
        {FILTER_KEYS.map((k) => (
          <input
            key={k}
            type="text"
            placeholder={k}
            value={draft[k]}
            onChange={(e) => setDraft((d) => ({ ...d, [k]: e.target.value }))}
          />
        ))}
      </div>

      <ErrorBanner error={list.error} />

      {list.isLoading ? (
        <div className="muted">Loading…</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Time</th>
              <th>Action</th>
              <th>Operator</th>
              <th>Target</th>
              <th>IP</th>
              <th>Details</th>
            </tr>
          </thead>
          <tbody>
            {(list.data?.items ?? []).map((e) => (
              <AuditRow key={e.id} e={e} />
            ))}
            {(list.data?.items ?? []).length === 0 && (
              <tr>
                <td colSpan={6} className="muted">
                  No audit rows match.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}

      <Pagination links={list.data?.links ?? {}} />
    </div>
  );
}

function AuditRow({ e }: { e: AuditEntry }) {
  return (
    <tr>
      <td>{new Date(e.ts).toLocaleString()}</td>
      <td>
        <code>{e.action}</code>
      </td>
      <td>{e.operator_id || <span className="muted">—</span>}</td>
      <td>
        {e.target_type && e.target_id ? (
          <TargetLink type={e.target_type} id={e.target_id} />
        ) : (
          <span className="muted">—</span>
        )}
      </td>
      <td>{e.ip || <span className="muted">—</span>}</td>
      <td>
        {e.details ? (
          <code style={{ fontSize: 11 }}>{JSON.stringify(e.details)}</code>
        ) : (
          <span className="muted">—</span>
        )}
      </td>
    </tr>
  );
}

function TargetLink({ type, id }: { type: string; id: string }) {
  // Best-effort linking to the related entity. Unknown target_type falls back
  // to a non-link rendering so the audit row still tells the operator what
  // was touched.
  switch (type) {
    case "account":
      return (
        <Link to={`/accounts/${id}`}>
          {type}:{id}
        </Link>
      );
    default:
      return (
        <span>
          {type}:{id}
        </span>
      );
  }
}
