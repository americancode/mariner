import { FormEvent, useEffect, useState } from "react";
import { LoaderCircle } from "lucide-react";
import { api, AuditEvent, Organization } from "../api";
import { errorMessage } from "../utils/errors";
import { promptUnlockOnReturn, signOut } from "../utils/navigation";

const auditColumns = [
  ["date", "Date"], ["user", "User"], ["action", "Action"],
  ["bucket", "Bucket"], ["object", "Object"], ["result", "Result"],
  ["sha256", "SHA-256"],
] as const;

export function AdminPage() {
  const auditPage = window.location.pathname.startsWith("/admin/audit");
  const organizationsPage = window.location.pathname.startsWith("/admin/organizations");
  return (
    <div className="admin-layout">
      <aside className="admin-sidebar">
        <a className="logo" href="/">mariner</a>
        <span className="eyebrow">ADMINISTRATION</span>
        <nav className="admin-nav" aria-label="Admin navigation">
          <a className={!auditPage && !organizationsPage ? "active" : ""} href="/admin">Overview</a>
          <a className={auditPage ? "active" : ""} href="/admin/audit">Audit logs</a>
          <a className={organizationsPage ? "active" : ""} href="/admin/organizations">Organizations</a>
        </nav>
        <a className="secondary admin-back" href="/" onClick={promptUnlockOnReturn}>Back to Mariner</a>
      </aside>
      <main className="admin-main">
        <a className="sign-out admin-sign-out" href="/auth/logout" onClick={signOut}>Sign out</a>
        {auditPage ? <AdminAuditPage /> : organizationsPage ? <AdminOrganizationsPage /> : <AdminOverviewPage />}
      </main>
    </div>
  );
}

function AdminOverviewPage() {
  const [maxGiB, setMaxGiB] = useState("5");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => { void api.adminSettings().then((settings) => setMaxGiB(String(settings.archiveMaxBytes / (1024 ** 3)))).catch((err) => setError(errorMessage(err))); }, []);
  async function save(event: FormEvent) { event.preventDefault(); const value = Number(maxGiB); if (!Number.isFinite(value) || value <= 0) { setError("Archive limit must be greater than zero."); return; } try { await api.updateAdminSettings({ archiveMaxBytes: Math.floor(value * 1024 ** 3) }); setSaved(true); setError(""); } catch (err) { setError(errorMessage(err)); } }
  return (
    <section className="admin-landing">
      <span className="eyebrow">ADMINISTRATION</span><h1>Mariner administration</h1>
      <p>Manage application activity and organization access from one workspace.</p>
      <div className="admin-cards">
        <a className="card admin-card" href="/admin/audit"><h2>Audit logs</h2><p>Search security and S3 activity with database-backed filters.</p></a>
        <a className="card admin-card" href="/admin/organizations"><h2>Organizations</h2><p>Configure groups and shared connections.</p></a>
      </div>
      <form className="card org-form" onSubmit={save}>
        <h2>Download limits</h2><p>Limit the total size of files included in generated ZIP and TGZ downloads.</p>
        <label>Maximum archive size (GiB)<input type="number" min="0.001" step="0.001" value={maxGiB} onChange={(event) => setMaxGiB(event.target.value)} /></label>
        <div className="modal-actions"><button className="button" type="submit">Save limit</button>{saved && <span className="test-success">Saved</span>}{error && <span className="field-error">{error}</span>}</div>
      </form>
    </section>
  );
}

function AdminOrganizationsPage() {
  const [organizations, setOrganizations] = useState<Organization[]>([]);
  const [editing, setEditing] = useState<Organization>();
  const [error, setError] = useState("");
  const empty: Organization = { id: "", name: "", groups: [], provisioned: false, connections: {} };
  async function refresh() { try { setOrganizations(await api.adminOrganizations()); } catch (err) { setError(errorMessage(err)); } }
  useEffect(() => { void refresh(); }, []);
  async function save(event: FormEvent) { event.preventDefault(); if (!editing) return; const normalized = { ...editing, connections: Object.fromEntries(Object.entries(editing.connections).map(([key, connection]) => [key, { ...connection, region: connection.region || "us-east-1" }])) }; try { if (organizations.some((item) => item.id === editing.id)) await api.updateOrganization(normalized); else await api.createOrganization(normalized); setEditing(undefined); await refresh(); } catch (err) { setError(errorMessage(err)); } }
  async function remove(id: string) { if (!window.confirm(`Delete organization ${id}?`)) return; try { await api.deleteOrganization(id); await refresh(); } catch (err) { setError(errorMessage(err)); } }
  return <section className="admin-page"><div className="admin-header"><div><span className="eyebrow">ADMINISTRATION</span><h1>Organizations</h1><p>Map OIDC groups to shared S3 connections.</p></div><button className="button" onClick={() => setEditing(empty)}>New organization</button></div>{error && <p className="form-error" role="alert">{error}</p>}<div className="org-list">{organizations.map((organization) => <article className="card org-card" key={organization.id}><div><h2>{organization.name}</h2><p><code>{organization.id}</code> · Groups: {organization.groups.join(", ") || "none"}</p><small>{Object.keys(organization.connections).length} connection(s){organization.provisioned ? " · Provisioned by Helm" : ""}</small></div><div className="org-actions">{organization.provisioned ? <span className="provisioned-label">Provisioned</span> : <><button className="secondary" onClick={() => setEditing({ ...organization, connections: { ...organization.connections } })}>Edit</button><button className="danger" onClick={() => void remove(organization.id)}>Delete</button></>}</div></article>)}</div>{editing && <OrganizationForm organization={editing} onChange={setEditing} onCancel={() => setEditing(undefined)} onSubmit={save} />}</section>;
}

function OrganizationForm({ organization, onChange, onCancel, onSubmit }: { organization: Organization; onChange: (value: Organization) => void; onCancel: () => void; onSubmit: (event: FormEvent) => void }) {
  const connections = Object.entries(organization.connections);
  const [testing, setTesting] = useState<string>();
  const [testResults, setTestResults] = useState<Record<string, string>>({});
  function updateConnection(name: string, value: Partial<Organization["connections"][string]>) { onChange({ ...organization, connections: { ...organization.connections, [name]: { ...organization.connections[name], ...value } } }); }
  return <form className="card org-form" onSubmit={onSubmit}><h2>{organization.id ? "Edit organization" : "New organization"}</h2><div className="form-grid"><label>ID<input required value={organization.id} disabled={organization.provisioned} onChange={(e) => onChange({ ...organization, id: e.target.value })} /></label><label>Name<input required value={organization.name} onChange={(e) => onChange({ ...organization, name: e.target.value })} /></label><label className="full-width">OIDC groups<span>comma-separated exact values</span><input value={organization.groups.join(", ")} onChange={(e) => onChange({ ...organization, groups: e.target.value.split(",").map((item) => item.trim()).filter(Boolean) })} /></label></div><div className="org-connections"><div className="org-section-heading"><h3>Default connections</h3><button type="button" className="secondary" onClick={() => { const name = `connection-${connections.length + 1}`; onChange({ ...organization, connections: { ...organization.connections, [name]: { id: name, name, bucket: "", region: "us-east-1", endpoint: "", prefix: "" } } }); }}>Add connection</button></div>{connections.map(([key, connection]) => <div className="connection-editor" key={key}><label>Name<input required value={connection.name} onChange={(e) => updateConnection(key, { name: e.target.value })} /></label><label>Bucket<input required value={connection.bucket} onChange={(e) => updateConnection(key, { bucket: e.target.value })} /></label><label>Endpoint<input value={connection.endpoint} onChange={(e) => updateConnection(key, { endpoint: e.target.value })} /></label><label>Region<input value={connection.region || "us-east-1"} onChange={(e) => updateConnection(key, { region: e.target.value })} placeholder="us-east-1" /></label><label>Access key<input type="text" placeholder="unchanged if blank" value={connection.accessKey || ""} onChange={(e) => updateConnection(key, { accessKey: e.target.value })} /></label><label>Secret key<input type="password" placeholder="unchanged if blank" value={connection.secretKey || ""} onChange={(e) => updateConnection(key, { secretKey: e.target.value })} /></label><div className="connection-test"><button type="button" className="secondary" disabled={testing === key || !connection.accessKey || !connection.secretKey} onClick={async () => { setTesting(key); setTestResults({ ...testResults, [key]: "Testing connection…" }); try { await api.testConnection({ ...connection, id: "", region: connection.region || "us-east-1" }); setTestResults({ ...testResults, [key]: "Connection successful" }); } catch (err) { setTestResults({ ...testResults, [key]: errorMessage(err) }); } finally { setTesting(undefined); } }}>{testing === key ? "Testing…" : "Test connection"}</button>{testResults[key] && <span className={testResults[key] === "Connection successful" ? "test-success" : "field-error"}>{testResults[key]}</span>}</div><button type="button" className="danger" onClick={() => { const next = { ...organization.connections }; delete next[key]; onChange({ ...organization, connections: next }); }}>Remove</button></div>)}</div><div className="modal-actions"><button type="button" className="secondary" onClick={onCancel}>Cancel</button><button className="button" type="submit">Save organization</button></div></form>;
}

function AdminAuditPage() {
  const [filters, setFilters] = useState({ user: "", bucket: "", action: "", start: "", end: "" });
  const [events, setEvents] = useState<AuditEvent[]>([]); const [hasMore, setHasMore] = useState(false); const [loading, setLoading] = useState(true); const [error, setError] = useState(""); const [activeFilters, setActiveFilters] = useState(filters); const [actionOptions, setActionOptions] = useState<string[]>([]); const [visibleColumns, setVisibleColumns] = useState<Record<string, boolean>>(Object.fromEntries(auditColumns.map(([key]) => [key, true]))); const [showColumns, setShowColumns] = useState(false);
  function localDateTime(value: string) { return value ? new Date(value).toISOString() : ""; }
  async function load(nextFilters = activeFilters, offset = 0) { setLoading(true); setError(""); try { const page = await api.audit(nextFilters, offset); setEvents((current) => (offset ? [...current, ...page.events] : page.events)); setHasMore(page.hasMore); } catch (err) { setError(errorMessage(err)); } finally { setLoading(false); } }
  useEffect(() => { void load(); api.auditActions().then(setActionOptions).catch((err) => setError(errorMessage(err))); api.adminPreferences().then((saved) => setVisibleColumns((current) => ({ ...current, ...saved }))).catch((err) => setError(errorMessage(err))); }, []);
  async function toggleColumn(key: string) { const next = { ...visibleColumns, [key]: !visibleColumns[key] }; if (Object.values(next).every((value) => !value)) return; setVisibleColumns(next); try { await api.updateAdminPreferences(next); } catch (err) { setError(errorMessage(err)); } }
  function submit(event: FormEvent) { event.preventDefault(); const nextFilters = { ...filters, start: localDateTime(filters.start), end: localDateTime(filters.end) }; setActiveFilters(nextFilters); void load(nextFilters); }
  function applyRange(minutes: number) { const end = new Date(); const start = new Date(end.getTime() - minutes * 60 * 1000); const toLocal = (date: Date) => { const offset = date.getTimezoneOffset() * 60000; return new Date(date.getTime() - offset).toISOString().slice(0, 16); }; setFilters({ ...filters, start: toLocal(start), end: toLocal(end) }); }
  return <section className="admin-page"><div className="admin-header"><div><span className="eyebrow">ADMINISTRATION</span><h1>Application audit log</h1><p>Search security and S3 activity from the database.</p></div><button className="secondary" type="button" onClick={() => setShowColumns(!showColumns)}>Columns</button></div>{showColumns && <div className="card column-picker" aria-label="Audit columns">{auditColumns.map(([key, label]) => <label key={key}><input type="checkbox" checked={visibleColumns[key]} onChange={() => void toggleColumn(key)} />{label}</label>)}</div>}<form className="card audit-filters" onSubmit={submit}><div className="form-grid"><label>User <span>(ID or name)</span><input value={filters.user} onChange={(e) => setFilters({ ...filters, user: e.target.value })} placeholder="user@example.com" /></label><label>Bucket<input value={filters.bucket} onChange={(e) => setFilters({ ...filters, bucket: e.target.value })} placeholder="bucket-name" /></label><label>Action<select value={filters.action} onChange={(e) => setFilters({ ...filters, action: e.target.value })}><option value="">All actions</option>{actionOptions.map((action) => <option key={action} value={action}>{action}</option>)}</select></label><label>From date and time<input type="datetime-local" value={filters.start} onChange={(e) => setFilters({ ...filters, start: e.target.value })} /></label><label>Through date and time<input type="datetime-local" value={filters.end} onChange={(e) => setFilters({ ...filters, end: e.target.value })} /></label></div><div className="audit-presets" aria-label="Time range presets"><span>Quick range</span><button type="button" className="secondary" onClick={() => applyRange(15)}>Last 15 minutes</button><button type="button" className="secondary" onClick={() => applyRange(60)}>Last hour</button><button type="button" className="secondary" onClick={() => applyRange(1440)}>Last day</button><button type="button" className="secondary" onClick={() => applyRange(10080)}>Last week</button></div><div className="modal-actions"><button className="button" type="submit" disabled={loading}>{loading && <LoaderCircle className="spin" size={15} />}Apply filters</button></div></form>{error && <p className="form-error" role="alert">{error}</p>}<section className="card audit-results">{loading && !events.length ? <div className="admin-loading"><LoaderCircle className="spin" size={22} />Loading audit events…</div> : events.length ? <AuditTable events={events} visibleColumns={visibleColumns} /> : <p className="admin-empty">No audit events match these filters.</p>}{hasMore && <button className="secondary load-more" onClick={() => void load(activeFilters, events.length)} disabled={loading}>{loading && <LoaderCircle className="spin" size={15} />}Load more</button>}</section></section>;
}

function AuditTable({ events, visibleColumns }: { events: AuditEvent[]; visibleColumns: Record<string, boolean> }) { return <div className="audit-table-wrap"><table className="audit-table"><thead><tr>{visibleColumns.date && <th>Date</th>}{visibleColumns.user && <th>User</th>}{visibleColumns.action && <th>Action</th>}{visibleColumns.bucket && <th>Bucket</th>}{visibleColumns.object && <th>Object</th>}{visibleColumns.result && <th>Result</th>}{visibleColumns.sha256 && <th>SHA-256</th>}</tr></thead><tbody>{events.map((event) => <tr key={event.event_id}>{visibleColumns.date && <td>{new Date(event.occurred_at).toLocaleString()}</td>}{visibleColumns.user && <td>{event.user_name || event.user_id || "—"}</td>}{visibleColumns.action && <td><code>{event.action}</code></td>}{visibleColumns.bucket && <td>{event.bucket || "—"}</td>}{visibleColumns.object && <td>{event.object_key || "—"}</td>}{visibleColumns.result && <td><span className={`audit-result ${event.result}`}>{event.result}</span></td>}{visibleColumns.sha256 && <td><code className="digest">{event.file_sha256 || "—"}</code></td>}</tr>)}</tbody></table></div>; }
