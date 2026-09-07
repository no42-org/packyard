/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */

import { useEffect, useState } from "react";

import {
  Component,
  useComponents,
  useCreateComponent,
  useUpdateComponent,
} from "../api/components";
import {
  type DeleteImpact,
  canConfirmDelete,
  previewComponentDelete,
  useDeleteComponent,
} from "../api/componentDelete";
import { ErrorBanner } from "../components/ErrorBanner";
import { Modal } from "../components/Modal";

export function Components() {
  const list = useComponents();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<Component | null>(null);
  const [deleting, setDeleting] = useState<Component | null>(null);

  return (
    <div>
      <div className="page-header">
        <h1 className="page-title">Components</h1>
        <button onClick={() => setCreateOpen(true)}>+ New component</button>
      </div>

      <ErrorBanner error={list.error} />

      {list.isLoading ? (
        <div className="muted">Loading…</div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Visibility</th>
              <th>RPM series</th>
              <th>OS families</th>
              <th>Architectures</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((c) => (
              <tr key={c.name}>
                <td>{c.name}</td>
                <td>
                  <span className="status-pill status-active">{c.visibility}</span>
                </td>
                <td>{c.rpm_series.join(", ")}</td>
                <td>{c.rpm_os_families.join(", ")}</td>
                <td>{c.rpm_architectures.join(", ")}</td>
                <td>
                  <button className="btn-secondary" onClick={() => setEditing(c)}>
                    Edit
                  </button>{" "}
                  <button className="btn-danger" onClick={() => setDeleting(c)}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
            {(list.data ?? []).length === 0 && (
              <tr>
                <td colSpan={6} className="muted">
                  No components configured.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}

      <CreateComponentModal open={createOpen} onClose={() => setCreateOpen(false)} />
      {editing && (
        <EditComponentModal component={editing} onClose={() => setEditing(null)} />
      )}
      {deleting && (
        <DeleteComponentModal component={deleting} onClose={() => setDeleting(null)} />
      )}
    </div>
  );
}

function CreateComponentModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const create = useCreateComponent();
  const [name, setName] = useState("");
  const [visibility, setVisibility] = useState<"public" | "private">("private");
  const [series, setSeries] = useState("");
  const [families, setFamilies] = useState("");
  const [archs, setArchs] = useState("");

  const submit = async () => {
    try {
      await create.mutateAsync({
        name,
        visibility,
        rpm_series: csv(series),
        rpm_os_families: csv(families),
        rpm_architectures: csv(archs),
      });
      onClose();
    } catch {
      /* error rendered below */
    }
  };

  return (
    <Modal
      open={open}
      title="New component"
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button onClick={submit} disabled={create.isPending || !name}>
            Create
          </button>
        </>
      }
    >
      <div className="field">
        <label>Name</label>
        <input type="text" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      <div className="field">
        <label>Visibility</label>
        <select value={visibility} onChange={(e) => setVisibility(e.target.value as "public" | "private")}>
          <option value="private">private</option>
          <option value="public">public</option>
        </select>
      </div>
      <div className="field">
        <label>RPM series (comma-separated, e.g. 33,34)</label>
        <input type="text" value={series} onChange={(e) => setSeries(e.target.value)} />
      </div>
      <div className="field">
        <label>OS families (comma-separated, e.g. rhel,alma,rocky)</label>
        <input type="text" value={families} onChange={(e) => setFamilies(e.target.value)} />
      </div>
      <div className="field">
        <label>Architectures (comma-separated, e.g. x86_64,aarch64)</label>
        <input type="text" value={archs} onChange={(e) => setArchs(e.target.value)} />
      </div>
      <ErrorBanner error={create.error} />
    </Modal>
  );
}

function EditComponentModal({ component, onClose }: { component: Component; onClose: () => void }) {
  const update = useUpdateComponent(component.name);
  const [visibility, setVisibility] = useState(component.visibility);
  const [series, setSeries] = useState(component.rpm_series.join(","));
  const [families, setFamilies] = useState(component.rpm_os_families.join(","));
  const [archs, setArchs] = useState(component.rpm_architectures.join(","));
  const [removed, setRemoved] = useState<string[] | null>(null);

  const submit = async () => {
    try {
      const result = await update.mutateAsync({
        visibility,
        rpm_series: csv(series),
        rpm_os_families: csv(families),
        rpm_architectures: csv(archs),
      });
      if (result.rpm_targets_removed.length > 0) {
        // Keep the dialog open: the operator should see what stays on disk.
        setRemoved(result.rpm_targets_removed);
        return;
      }
      onClose();
    } catch {
      /* error rendered below */
    }
  };

  return (
    <Modal
      open={true}
      title={`Edit ${component.name}`}
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            {removed ? "Close" : "Cancel"}
          </button>
          {!removed && (
            <button onClick={submit} disabled={update.isPending}>
              Save
            </button>
          )}
        </>
      }
    >
      {removed ? (
        <div className="field">
          <p>Saved. These RPM targets are no longer declared on the component:</p>
          <ul>
            {removed.map((t) => (
              <li key={t}>
                <code>{t}</code>
              </li>
            ))}
          </ul>
          <p className="muted">
            Their directories and packages remain on disk and keep being served. Remove them by hand if
            they should disappear.
          </p>
        </div>
      ) : (
        <>
          <div className="field">
            <label>Visibility</label>
            <select
              value={visibility}
              onChange={(e) => setVisibility(e.target.value as "public" | "private")}
            >
              <option value="private">private</option>
              <option value="public">public</option>
            </select>
          </div>
          <div className="field">
            <label>RPM series (comma-separated, e.g. 38,39)</label>
            <input type="text" value={series} onChange={(e) => setSeries(e.target.value)} />
          </div>
          <div className="field">
            <label>OS families (comma-separated, e.g. el9,el10)</label>
            <input type="text" value={families} onChange={(e) => setFamilies(e.target.value)} />
          </div>
          <div className="field">
            <label>Architectures (comma-separated, e.g. x86_64,aarch64)</label>
            <input type="text" value={archs} onChange={(e) => setArchs(e.target.value)} />
          </div>
          <p className="muted">
            New series, OS family and architecture combinations are provisioned on save. Removing one
            leaves its directory on disk.
          </p>
          <ErrorBanner error={update.error} />
        </>
      )}
    </Modal>
  );
}

// DeleteComponentModal drives the API's safe-lock: first the preview (the 409
// impact), then the destructive call only once the operator has typed the
// component name exactly. The API enforces the same rule; this mirrors it so
// the UI cannot skip the preview.
function DeleteComponentModal({ component, onClose }: { component: Component; onClose: () => void }) {
  const del = useDeleteComponent(component.name);
  const [impact, setImpact] = useState<DeleteImpact | null>(null);
  const [previewError, setPreviewError] = useState<unknown>(null);
  const [typed, setTyped] = useState("");

  useEffect(() => {
    let cancelled = false;
    previewComponentDelete(component.name)
      .then((i) => {
        if (!cancelled) setImpact(i);
      })
      .catch((e: unknown) => {
        if (!cancelled) setPreviewError(e);
      });
    return () => {
      cancelled = true;
    };
  }, [component.name]);

  const submit = async () => {
    try {
      await del.mutateAsync();
      onClose();
    } catch {
      /* error rendered below */
    }
  };

  const ready = impact !== null && canConfirmDelete(typed, component.name) && !del.isPending;

  return (
    <Modal
      open={true}
      title={`Delete ${component.name}`}
      onClose={onClose}
      actions={
        <>
          <button className="btn-secondary" onClick={onClose}>
            Cancel
          </button>
          <button className="btn-danger" onClick={submit} disabled={!ready}>
            Delete component
          </button>
        </>
      }
    >
      {impact === null && previewError === null && <p className="muted">Loading impact…</p>}
      {impact !== null && (
        <div className="field">
          <p>Deleting this component will:</p>
          <ul>
            <li>
              revoke <strong>{impact.keys_revoked}</strong> active subscription key
              {impact.keys_revoked === 1 ? "" : "s"}
            </li>
            <li>
              drop the auth records for RPM series{" "}
              {impact.rpm_series_removed.length > 0 ? (
                impact.rpm_series_removed.map((sName) => <code key={sName}>{sName} </code>)
              ) : (
                <em>none</em>
              )}
            </li>
          </ul>
          <p className="muted">
            Published packages and directories stay on disk and keep being served by the web tier;
            remove them by hand if they should disappear. This cannot be undone.
          </p>
          <label>
            Type <code>{component.name}</code> to confirm
          </label>
          <input
            type="text"
            value={typed}
            autoComplete="off"
            onChange={(e) => setTyped(e.target.value)}
          />
        </div>
      )}
      <ErrorBanner error={previewError ?? del.error} />
    </Modal>
  );
}

function csv(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}
