import { useState } from "react";

import {
  Component,
  useComponents,
  useCreateComponent,
  useUpdateComponent,
} from "../api/components";
import { ErrorBanner } from "../components/ErrorBanner";
import { Modal } from "../components/Modal";

export function Components() {
  const list = useComponents();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<Component | null>(null);

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

  const submit = async () => {
    try {
      await update.mutateAsync({ visibility });
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
            Cancel
          </button>
          <button onClick={submit} disabled={update.isPending}>
            Save
          </button>
        </>
      }
    >
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
      <p className="muted">
        Other fields (series / OS families / architectures) are immutable per the components API.
      </p>
      <ErrorBanner error={update.error} />
    </Modal>
  );
}

function csv(s: string): string[] {
  return s
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}
