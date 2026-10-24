import { FormEvent, useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  Check,
  ChevronDown,
  Download,
  Eye,
  EyeOff,
  File,
  Folder,
  FolderPlus,
  LoaderCircle,
  LockKeyhole,
  Moon,
  MoreVertical,
  Pencil,
  Plus,
  RefreshCw,
  Shield,
  Trash2,
  Upload,
  Sun,
  UnlockKeyhole,
  X,
} from "lucide-react";
import { api, Connection, ConnectionGroup, Item, MultipartUpload, ObjectVersion, OrganizationOption } from "./api";
import { AdminPage as ModularAdminPage } from "./pages/AdminPage";
import { errorMessage } from "./utils/errors";
import { exactBytes, formatBytes } from "./utils/format";
import { consumePromptUnlockAfterSignIn, signOut } from "./utils/navigation";
import { useTheme } from "./hooks/useTheme";

type User = { authenticated: boolean; name?: string; isAdmin?: boolean };
type ErrorHandler = (message: string) => void;
type ItemFilter = { files: boolean; folders: boolean; multipart: boolean };
const defaultItemFilter: ItemFilter = {
  files: true,
  folders: true,
  multipart: false,
};
function browseKind(filter: ItemFilter): "all" | "file" | "folder" {
  if (filter.files && !filter.folders) return "file";
  if (filter.folders && !filter.files) return "folder";
  return "all";
}
function multipartSelectionKey(upload: MultipartUpload) {
  return `multipart:${upload.uploadId}`;
}
function sameItemFilter(left: ItemFilter, right: ItemFilter) {
  return (
    left.files === right.files &&
    left.folders === right.folders &&
    left.multipart === right.multipart
  );
}
type ConnectionForm = {
  id?: string;
  name: string;
  bucket: string;
  region: string;
  prefix: string;
  endpoint: string;
  accessKey: string;
  secretKey: string;
  organization: string;
  groupPath: string;
};
type Activity = {
  id: string;
  label: string;
  context: string;
  kind: "upload" | "delete" | "download";
  progress: number;
  phase?: "uploading" | "finalizing";
  state: "active" | "done" | "error" | "canceled";
  error?: string;
  finishedAt?: number;
};
type ActivitySetter = React.Dispatch<React.SetStateAction<Activity[]>>;
export function App() {
  const [user, setUser] = useState<User>();
  const [connections, setConnections] = useState<Connection[]>([]);
  const [organizations, setOrganizations] = useState<OrganizationOption[]>([]);
  const [connectionGroups, setConnectionGroups] = useState<ConnectionGroup[]>([]);
  const [groupModalOpen, setGroupModalOpen] = useState(false);
  const [groupRenameTarget, setGroupRenameTarget] = useState<ConnectionGroup>();
  const [activeConnection, setActiveConnection] = useState<Connection>();
  const [prefix, setPrefix] = useState("");
  const [items, setItems] = useState<Item[]>([]);
  const [itemFilter, setItemFilter] = useState<ItemFilter>(defaultItemFilter);
  const [searchQuery, setSearchQuery] = useState("");
  const [nextToken, setNextToken] = useState("");
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [unlockOpen, setUnlockOpen] = useState(false);
  const [vaultExists, setVaultExists] = useState<boolean>();
  const [vaultUnlocked, setVaultUnlocked] = useState(false);
  const [connectionModalOpen, setConnectionModalOpen] = useState(false);
  const [editingConnection, setEditingConnection] = useState<Connection>();
  const [connectionDeleteTarget, setConnectionDeleteTarget] =
    useState<Connection>();
  const { darkMode, setTheme } = useTheme();
  const currentView = useRef({
    activeConnection,
    prefix,
    itemFilter,
    searchQuery,
  });
  currentView.current = { activeConnection, prefix, itemFilter, searchQuery };

  useEffect(() => {
    api
      .me()
      .then(setUser)
      .catch((err) => setError(errorMessage(err)));
  }, []);
  useEffect(() => {
    if (user && !user.authenticated) window.location.assign("/auth/login");
  }, [user]);
  useEffect(() => {
    if (!vaultUnlocked) { setOrganizations([]); return; }
    Promise.all([api.organizations(), api.connectionGroups()])
      .then(([nextOrganizations, nextGroups]) => { setOrganizations(nextOrganizations); setConnectionGroups(nextGroups); })
      .catch((err) => setError(errorMessage(err)));
  }, [vaultUnlocked]);
  useEffect(() => {
    if (
      !user?.authenticated ||
      window.location.pathname === "/admin" ||
      window.location.pathname.startsWith("/admin/")
    )
      return;
    api
      .status()
      .then(({ exists }) => {
        setVaultExists(exists);
        const promptAfterSignIn = consumePromptUnlockAfterSignIn();
        setUnlockOpen(!exists || promptAfterSignIn);
      })
      .catch((err) => setError(errorMessage(err)));
  }, [user]);

  async function browse(
    connection: Connection,
    nextPrefix = "",
    kind = itemFilter,
  ) {
    setActiveConnection(connection);
    setPrefix(nextPrefix);
    setSearchQuery("");
    setLoading(true);
    try {
      const result = await api.browse(
        connection.id,
        nextPrefix,
        browseKind(kind),
      );
      setItems(result.items);
      setNextToken(result.nextToken ?? "");
      setHasMore(result.hasMore);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }
  async function refreshCurrentView() {
    const view = currentView.current;
    if (!view.activeConnection) return;
    try {
      if (view.searchQuery.trim()) {
        const result = await api.search(
          view.activeConnection.id,
          view.prefix,
          view.searchQuery,
          browseKind(view.itemFilter),
        );
        const latestView = currentView.current;
        if (
          latestView.activeConnection?.id !== view.activeConnection.id ||
          latestView.prefix !== view.prefix ||
          !sameItemFilter(latestView.itemFilter, view.itemFilter) ||
          latestView.searchQuery !== view.searchQuery
        )
          return;
        setItems(result.items);
        setNextToken("");
        setHasMore(false);
        return;
      }
      const result = await api.browse(
        view.activeConnection.id,
        view.prefix,
        browseKind(view.itemFilter),
      );
      const latestView = currentView.current;
      if (
        latestView.activeConnection?.id !== view.activeConnection.id ||
        latestView.prefix !== view.prefix ||
        !sameItemFilter(latestView.itemFilter, view.itemFilter)
      ) {
        return;
      }
      setItems(result.items);
      setNextToken(result.nextToken ?? "");
      setHasMore(result.hasMore);
    } catch (err) {
      setError(errorMessage(err));
    }
  }
  async function loadMore() {
    if (!activeConnection || !hasMore || loading || searchQuery.trim()) return;
    setLoadingMore(true);
    try {
      const result = await api.browse(
        activeConnection.id,
        prefix,
        browseKind(itemFilter),
        nextToken,
      );
      setItems((current) => [...current, ...result.items]);
      setNextToken(result.nextToken ?? "");
      setHasMore(result.hasMore);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoadingMore(false);
    }
  }

  async function addConnection(value: ConnectionForm) {
    try {
      const created = await api.addConnection(value);
      const nextConnections = await api.connections();
      setConnections(nextConnections);
      setConnectionGroups(await api.connectionGroups());
      setConnectionModalOpen(false);
      const saved = nextConnections.find(
        (connection) => connection.id === created.id,
      );
      if (saved) await browse(saved);
    } catch (err) {
      setError(errorMessage(err));
    }
  }
  async function updateConnection(value: ConnectionForm) {
    try {
      await api.updateConnection(value);
      const nextConnections = await api.connections();
      setConnections(nextConnections);
      setConnectionGroups(await api.connectionGroups());
      setEditingConnection(undefined);
      if (activeConnection?.id === value.id) {
        setActiveConnection(undefined);
        setItems([]);
        setPrefix("");
      }
      const saved = nextConnections.find(
        (connection) => connection.id === value.id,
      );
      if (saved) await browse(saved, prefix);
    } catch (err) {
      setError(errorMessage(err));
    }
  }
  async function deleteConnection(connection: Connection) {
    setConnectionDeleteTarget(connection);
  }
  async function confirmDeleteConnection() {
    if (!connectionDeleteTarget) return;
    try {
      await api.deleteConnection(connectionDeleteTarget.id);
      const nextConnections = await api.connections();
      setConnections(nextConnections);
      if (activeConnection?.id === connectionDeleteTarget.id) {
        setActiveConnection(undefined);
        setItems([]);
        setPrefix("");
      }
      setConnectionDeleteTarget(undefined);
    } catch (err) {
      setError(errorMessage(err));
    }
  }
  async function createConnectionGroup(organization: string, path: string) {
    try {
      await api.createConnectionGroup({ organization: organization || undefined, path });
      setConnectionGroups(await api.connectionGroups());
      setGroupModalOpen(false);
    } catch (err) { setError(errorMessage(err)); }
  }
  async function deleteConnectionGroup(group: ConnectionGroup) {
    try { await api.deleteConnectionGroup(group); setConnectionGroups(await api.connectionGroups()); }
    catch (err) { setError(errorMessage(err)); }
  }
  async function renameConnectionGroup(group: ConnectionGroup, newPath: string) {
    try {
      await api.renameConnectionGroup({ ...group, newPath });
      const replacePath = (path: string) => path === group.path ? newPath : path.startsWith(`${group.path}/`) ? `${newPath}${path.slice(group.path.length)}` : path;
      setConnectionGroups((current) => current.map((item) => item.organization === group.organization && (item.path === group.path || item.path.startsWith(`${group.path}/`)) ? { ...item, path: replacePath(item.path) } : item));
      setConnections((current) => current.map((connection) => connection.organization === group.organization && connection.groupPath && (connection.groupPath === group.path || connection.groupPath.startsWith(`${group.path}/`)) ? { ...connection, groupPath: replacePath(connection.groupPath) } : connection));
      setGroupRenameTarget(undefined);
    }
    catch (err) { setError(errorMessage(err)); }
  }

  if (!user) return <main className="center">Loading…</main>;
  if (!user.authenticated)
    return <main className="center">Redirecting to sign in…</main>;
  if (window.location.pathname.startsWith("/admin"))
    return <ModularAdminPage />;
  return (
    <div className="layout">
      <Sidebar
        userName={user.name}
        isAdmin={user.isAdmin}
        connections={connections}
        activeConnection={activeConnection}
        onBrowse={browse}
        onAddConnection={() => setConnectionModalOpen(true)}
        onEditConnection={setEditingConnection}
        onDeleteConnection={deleteConnection}
        organizations={organizations}
        groups={connectionGroups}
        onNewGroup={() => setGroupModalOpen(true)}
        onDeleteGroup={deleteConnectionGroup}
        onRenameGroup={setGroupRenameTarget}
        darkMode={darkMode}
        vaultUnlocked={vaultUnlocked}
        onUnlock={() => setUnlockOpen(true)}
        onToggleTheme={async () => {
          const theme = darkMode ? "light" : "dark";
          setTheme(theme);
          if (vaultUnlocked) await api.updateSettings({ theme });
        }}
      />
      <Workspace
        connection={activeConnection}
        loading={loading}
        prefix={prefix}
        items={items}
        onBrowse={browse}
        onRefresh={refreshCurrentView}
        onError={setError}
        hasMore={hasMore}
        onLoadMore={loadMore}
        loadingMore={loadingMore}
        itemFilter={itemFilter}
        searchQuery={searchQuery}
        vaultUnlocked={vaultUnlocked}
        onUnlock={() => setUnlockOpen(true)}
        onSearchChange={(query) => {
          setSearchQuery(query);
          if (!activeConnection) return;
          if (query.trim()) {
            setLoading(true);
            api
              .search(
                activeConnection.id,
                prefix,
                query,
                browseKind(itemFilter),
              )
              .then((result) => {
                setItems(result.items);
                setNextToken("");
                setHasMore(false);
              })
              .catch((err) => setError(errorMessage(err)))
              .finally(() => setLoading(false));
          } else {
            browse(activeConnection, prefix, itemFilter);
          }
        }}
        onFilterChange={(nextFilter) => {
          setItemFilter(nextFilter);
          if (activeConnection) {
            if (searchQuery.trim()) {
              setLoading(true);
              api
                .search(
                  activeConnection.id,
                  prefix,
                  searchQuery,
                  browseKind(nextFilter),
                )
                .then((result) => {
                  setItems(result.items);
                  setNextToken("");
                  setHasMore(false);
                })
                .catch((err) => setError(errorMessage(err)))
                .finally(() => setLoading(false));
            } else browse(activeConnection, prefix, nextFilter);
          }
        }}
      />
      {unlockOpen && (
        <UnlockModal
          onDismiss={() => setUnlockOpen(false)}
          onDestroy={async () => {
            await api.destroyVault();
            setVaultExists(false);
            setVaultUnlocked(false);
            setConnections([]);
            setConnectionGroups([]);
            setActiveConnection(undefined);
            setPrefix("");
            setItems([]);
          }}
          onUnlock={async (password) => {
            try {
              setConnections(await api.unlock(password));
              setVaultExists(true);
              setVaultUnlocked(true);
              const theme =
                (await api.settings()).theme === "dark" ? "dark" : "light";
              setTheme(theme);
              setUnlockOpen(false);
            } catch (err) {
              throw err;
            }
          }}
          initialSetup={vaultExists === false}
        />
      )}
      {connectionModalOpen && (
        <ConnectionModal
          onCancel={() => setConnectionModalOpen(false)}
          onSubmit={addConnection}
          onTest={api.testConnection}
          organizations={organizations}
          groups={connectionGroups}
        />
      )}
      {editingConnection && (
        <ConnectionModal
          connection={editingConnection}
          onCancel={() => setEditingConnection(undefined)}
          onSubmit={updateConnection}
          onTest={api.testConnection}
          organizations={organizations}
          groups={connectionGroups}
        />
      )}
      {connectionDeleteTarget && (
        <DeleteConnectionModal
          connection={connectionDeleteTarget}
          onCancel={() => setConnectionDeleteTarget(undefined)}
          onConfirm={confirmDeleteConnection}
        />
      )}
      {groupModalOpen && (
        <ConnectionGroupModal
          organizations={organizations}
          onCancel={() => setGroupModalOpen(false)}
          onSubmit={createConnectionGroup}
        />
      )}
      {groupRenameTarget && (
        <RenameConnectionGroupModal
          group={groupRenameTarget}
          onCancel={() => setGroupRenameTarget(undefined)}
          onSubmit={(newPath) => renameConnectionGroup(groupRenameTarget, newPath)}
        />
      )}
      {error && (
        <div className="toast" onClick={() => setError("")}>
          {error}
        </div>
      )}
    </div>
  );
}
function Login() {
  return (
    <main className="center">
      <section className="card login">
        <div className="logo">mariner</div>
        <h1>Private S3, clearly managed.</h1>
        <p>
          Sign in with your identity provider to access your encrypted bucket
          connections.
        </p>
        <a className="button" href="/auth/login">
          Continue with OIDC
        </a>
      </section>
    </main>
  );
}

type ConnectionTreeNode = { name: string; path: string; children: Map<string, ConnectionTreeNode>; connections: Connection[] };

function ConnectionTree({ scope, label, organization, connections, groups, activeConnection, vaultUnlocked, onBrowse, onEditConnection, onDeleteConnection, onDeleteGroup, onRenameGroup }: {
  scope: string;
  label: string;
  organization: string;
  connections: Connection[];
  groups: string[];
  activeConnection?: Connection;
  vaultUnlocked: boolean;
  onBrowse: (connection: Connection) => void;
  onEditConnection: (connection: Connection) => void;
  onDeleteConnection: (connection: Connection) => void;
  onDeleteGroup: (group: ConnectionGroup) => void;
  onRenameGroup: (group: ConnectionGroup) => void;
}) {
  const [open, setOpen] = useState<Record<string, boolean>>({ [scope]: true });
  const root: ConnectionTreeNode = { name: label, path: "", children: new Map(), connections: [] };
  function ensure(path: string) {
    let node = root;
    let current = "";
    for (const name of path ? path.split("/") : []) {
      current = current ? `${current}/${name}` : name;
      if (!node.children.has(name)) node.children.set(name, { name, path: current, children: new Map(), connections: [] });
      node = node.children.get(name)!;
    }
    return node;
  }
  groups.forEach(ensure);
  connections.forEach((connection) => (connection.groupPath ? ensure(connection.groupPath).connections : root.connections).push(connection));
  const renderConnection = (connection: Connection, depth: number) => <button disabled={!vaultUnlocked} style={{ paddingLeft: `${18 + depth * 16}px` }} className={activeConnection?.id === connection.id ? "connection active" : "connection"} onClick={() => onBrowse(connection)} key={connection.id}>
    <span className="connection-label">{connection.name}</span>
    {(!connection.organization || connection.organizationCanManage) && <span className="connection-actions"><span role="button" tabIndex={0} aria-label={`Edit ${connection.name}`} onClick={(event) => { event.stopPropagation(); onEditConnection(connection); }} onKeyDown={(event) => { if (event.key === "Enter") { event.stopPropagation(); onEditConnection(connection); } }}><Pencil size={14} /></span><span role="button" tabIndex={0} aria-label={`Delete ${connection.name}`} onClick={(event) => { event.stopPropagation(); onDeleteConnection(connection); }} onKeyDown={(event) => { if (event.key === "Enter") onDeleteConnection(connection); }}><Trash2 size={14} /></span></span>}
  </button>;
  function renderNode(node: ConnectionTreeNode, depth: number) {
    const children = Array.from(node.children.values()).sort((a, b) => a.name.localeCompare(b.name));
    const isOpen = node.path ? open[node.path] === true : open[scope] !== false;
    return <div key={node.path || scope} className="connection-tree-node">
      {(node.path || node === root) && <button className="connection-group" style={{ paddingLeft: `${10 + depth * 12}px` }} onClick={() => setOpen((current) => ({ ...current, [node.path || scope]: !isOpen }))}><ChevronDown className={isOpen ? "" : "collapsed"} size={14} /><Folder size={14} />{node.name}<small>{node.connections.length + children.length}</small>{node.path && <><span className="connection-group-action" role="button" tabIndex={0} aria-label={`Rename ${node.name} group`} onClick={(event) => { event.stopPropagation(); onRenameGroup({ organization: organization || undefined, path: node.path }); }} onKeyDown={(event) => { if (event.key === "Enter") { event.stopPropagation(); onRenameGroup({ organization: organization || undefined, path: node.path }); } }}><Pencil size={13} /></span><span className="connection-group-action" role="button" tabIndex={0} aria-label={`Delete ${node.name} group`} onClick={(event) => { event.stopPropagation(); onDeleteGroup({ organization: organization || undefined, path: node.path }); }} onKeyDown={(event) => { if (event.key === "Enter") { event.stopPropagation(); onDeleteGroup({ organization: organization || undefined, path: node.path }); } }}><Trash2 size={13} /></span></>}</button>}
      {isOpen && <div className={node.path || node === root ? "connection-tree-children nested" : "connection-tree-children"}>{node.connections.sort((a, b) => a.name.localeCompare(b.name)).map((connection) => renderConnection(connection, node.path ? node.path.split("/").length : 0))}{children.map((child) => renderNode(child, depth + 1))}</div>}
    </div>;
  }
  return <section className="connection-scope">{renderNode(root, 0)}</section>;
}

function Sidebar({
  userName,
  isAdmin,
  connections,
  activeConnection,
  onBrowse,
  onAddConnection,
  onEditConnection,
  onDeleteConnection,
  organizations,
  groups,
  onNewGroup,
  onDeleteGroup,
  onRenameGroup,
  darkMode,
  vaultUnlocked,
  onUnlock,
  onToggleTheme,
}: {
  userName?: string;
  isAdmin?: boolean;
  connections: Connection[];
  activeConnection?: Connection;
  onBrowse: (connection: Connection) => void;
  onAddConnection: () => void;
  onEditConnection: (connection: Connection) => void;
  onDeleteConnection: (connection: Connection) => void;
  organizations: OrganizationOption[];
  groups: ConnectionGroup[];
  onNewGroup: () => void;
  onDeleteGroup: (group: ConnectionGroup) => void;
  onRenameGroup: (group: ConnectionGroup) => void;
  darkMode: boolean;
  vaultUnlocked: boolean;
  onUnlock: () => void;
  onToggleTheme: () => void;
}) {
  return (
    <aside>
      <div className="logo">mariner</div>
      <span className="eyebrow">SIGNED IN AS</span>
      <p>{userName?.trim() || "OIDC user"}</p>
      <span className="eyebrow">CONNECTIONS</span>
      <div className="connection-list">
        <ConnectionTree scope="personal" label="Personal" organization="" connections={connections.filter((connection) => !connection.organization)} groups={groups.filter((group) => !group.organization).map((group) => group.path)} activeConnection={activeConnection} vaultUnlocked={vaultUnlocked} onBrowse={onBrowse} onEditConnection={onEditConnection} onDeleteConnection={onDeleteConnection} onDeleteGroup={onDeleteGroup} onRenameGroup={onRenameGroup} />
        {organizations.map((organization) => <ConnectionTree key={organization.id} scope={organization.id} label={organization.name} organization={organization.id} connections={connections.filter((connection) => connection.organization === organization.id)} groups={groups.filter((group) => group.organization === organization.id).map((group) => group.path)} activeConnection={activeConnection} vaultUnlocked={vaultUnlocked} onBrowse={onBrowse} onEditConnection={onEditConnection} onDeleteConnection={onDeleteConnection} onDeleteGroup={onDeleteGroup} onRenameGroup={onRenameGroup} />)}
      </div>
      <button
        className="secondary add"
        disabled={!vaultUnlocked}
        title={
          vaultUnlocked
            ? "Add a bucket connection"
            : "Unlock your vault to add a connection"
        }
        onClick={onAddConnection}
      >
        <Plus size={16} /> Add connection
      </button>
      <button className="secondary add" disabled={!vaultUnlocked} onClick={onNewGroup}>
        <FolderPlus size={16} /> New group
      </button>
      <div className="sidebar-footer">
        {isAdmin && (
          <button
            className="lock admin-link"
            onClick={() => window.location.assign("/admin")}
          >
            <Shield size={15} /> Admin
          </button>
        )}
        <button className="lock theme-toggle" onClick={onToggleTheme}>
          {darkMode ? <Sun size={15} /> : <Moon size={15} />}
          {darkMode ? "Light theme" : "Dark theme"}
        </button>
        <button
          className="lock"
          onClick={() => {
            if (vaultUnlocked) {
              void api.lock().then(() => window.location.reload());
            } else {
              onUnlock();
            }
          }}
        >
          {vaultUnlocked ? (
            <LockKeyhole size={15} />
          ) : (
            <UnlockKeyhole size={15} />
          )}
          {vaultUnlocked ? "Lock vault" : "Unlock vault"}
        </button>
      </div>
    </aside>
  );
}

function Workspace({
  connection,
  loading,
  prefix,
  items,
  onBrowse,
  onRefresh,
  onError,
  hasMore,
  onLoadMore,
  loadingMore,
  itemFilter,
  searchQuery,
  onSearchChange,
  vaultUnlocked,
  onUnlock,
  onFilterChange,
}: {
  connection?: Connection;
  loading: boolean;
  prefix: string;
  items: Item[];
  onBrowse: (connection: Connection, prefix?: string) => void;
  onRefresh: () => Promise<void>;
  onError: ErrorHandler;
  hasMore: boolean;
  onLoadMore: () => Promise<void>;
  loadingMore: boolean;
  itemFilter: ItemFilter;
  searchQuery: string;
  onSearchChange: (query: string) => void;
  vaultUnlocked: boolean;
  onUnlock: () => void;
  onFilterChange: (filter: ItemFilter) => void;
}) {
  const [activities, setActivities] = useState<Activity[]>([]);
  const uploadControllers = useRef(new Map<string, AbortController>());
  function cancelUpload(id: string) {
    uploadControllers.current.get(id)?.abort();
  }
  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now();
      setActivities((current) => {
        const next = current.filter((activity) => {
          if (activity.state === "active" || !activity.finishedAt) return true;
          const lifetime = activity.state === "done" ? 30_000 : 60_000;
          return now - activity.finishedAt < lifetime;
        });
        return next.length === current.length ? current : next;
      });
    }, 1_000);
    return () => window.clearInterval(timer);
  }, []);

  return (
    <main className="content">
      <header>
        <div>
          <span className="eyebrow">BUCKET EXPLORER</span>
          <h1>{connection?.name ?? "Your workspace"}</h1>
        </div>
        <a className="sign-out" href="/auth/logout" onClick={signOut}>
          Sign out
        </a>
      </header>
      {connection ? (
        loading ? (
          <section className="card loading-state">
            <LoaderCircle className="spin" size={28} />
            <strong>Loading bucket contents</strong>
            <p>Connecting to {connection.bucket}…</p>
          </section>
        ) : (
          <Explorer
            connection={connection}
            prefix={prefix}
            items={items}
            setActivities={setActivities}
            uploadControllers={uploadControllers}
            onBrowse={onBrowse}
            onRefresh={onRefresh}
            onError={onError}
            hasMore={hasMore}
            onLoadMore={onLoadMore}
            loadingMore={loadingMore}
            itemFilter={itemFilter}
            searchQuery={searchQuery}
            onSearchChange={onSearchChange}
            onFilterChange={onFilterChange}
          />
        )
      ) : (
        <section className="card empty">
          {vaultUnlocked ? <Folder size={38} /> : <UnlockKeyhole size={38} />}
          <h2>{vaultUnlocked ? "Choose a connection" : "Unlock your vault"}</h2>
          <p>
            {vaultUnlocked
              ? "Select a bucket from the sidebar or add your first one."
              : "Unlock your vault to load your saved connections and manage buckets."}
          </p>
          {!vaultUnlocked && (
            <button className="button empty-action" onClick={onUnlock}>
              <UnlockKeyhole size={16} /> Unlock vault
            </button>
          )}
        </section>
      )}
      <ActivityTray
        activities={activities}
        onCancel={cancelUpload}
        onDismiss={(id) =>
          setActivities((current) => current.filter((item) => item.id !== id))
        }
      />
    </main>
  );
}

function UnlockModal({
  onUnlock,
  onDestroy,
  initialSetup,
  onDismiss,
}: {
  onUnlock: (password: string) => Promise<void>;
  onDestroy: () => Promise<void>;
  initialSetup: boolean;
  onDismiss: () => void;
}) {
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [visible, setVisible] = useState(false);
  const [confirmDestroy, setConfirmDestroy] = useState(false);
  const [validationError, setValidationError] = useState("");
  const [unlockError, setUnlockError] = useState("");
  const [unlocking, setUnlocking] = useState(false);
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!password) return;
    if (initialSetup && password !== confirmation) {
      setValidationError("The master passwords do not match.");
      return;
    }
    setValidationError("");
    setUnlockError("");
    setUnlocking(true);
    try {
      await onUnlock(password);
    } catch (err) {
      setUnlockError(errorMessage(err));
      setUnlocking(false);
    }
  }
  return (
    <div className="modal-backdrop">
      <form className="card unlock" onSubmit={submit}>
        {!initialSetup && (
          <button
            type="button"
            className="modal-close unlock-close"
            aria-label="Close unlock dialog"
            onClick={onDismiss}
            disabled={unlocking}
          >
            <X size={20} />
          </button>
        )}
        <span className="eyebrow">
          {initialSetup ? "FIRST-TIME SETUP" : "PRIVATE VAULT"}
        </span>
        <h2>{initialSetup ? "Create your vault" : "Unlock your vault"}</h2>
        <p>
          {initialSetup
            ? "Choose a master password to create your encrypted vault. It protects the S3 connections saved by your account, and Mariner cannot recover it."
            : "Enter your master password to decrypt and access your saved S3 connections."}
        </p>
        {initialSetup && (
          <p className="unlock-note">
            Use at least 10 characters. You’ll use this password each time you
            unlock your vault.
          </p>
        )}
        <label className="unlock-label">
          {initialSetup ? "Master password" : "Enter master password"}
          <div className="password-field">
            <input
              autoFocus
              disabled={unlocking}
              type={visible ? "text" : "password"}
              value={password}
              onChange={(event) => {
                setPassword(event.target.value);
                setValidationError("");
                setUnlockError("");
              }}
              placeholder="Master password"
              minLength={10}
              required
            />
            <button
              type="button"
              className="icon password-toggle"
              disabled={unlocking}
              aria-label={visible ? "Hide password" : "Show password"}
              onClick={() => setVisible((value) => !value)}
            >
              {visible ? <EyeOff size={18} /> : <Eye size={18} />}
            </button>
          </div>
        </label>
        {initialSetup && (
          <label className="unlock-label">
            Retype master password
            <input
              type={visible ? "text" : "password"}
              disabled={unlocking}
              value={confirmation}
              onChange={(event) => {
                setConfirmation(event.target.value);
                setValidationError("");
                setUnlockError("");
              }}
              placeholder="Retype master password"
              minLength={10}
              required
            />
          </label>
        )}
        {validationError && <p className="form-error">{validationError}</p>}
        {unlockError && (
          <p className="form-error" role="alert">
            {unlockError}
          </p>
        )}
        <button className="button" type="submit" disabled={unlocking}>
          {unlocking && <LoaderCircle className="spin" size={17} />}
          {unlocking
            ? initialSetup
              ? "Creating encrypted vault…"
              : "Fetching and decrypting vault…"
            : initialSetup
              ? "Create encrypted vault"
              : "Unlock vault"}
        </button>
        {unlocking && (
          <p className="unlock-progress" role="status" aria-live="polite">
            <LoaderCircle className="spin" size={16} />
            Retrieving your encrypted vault and loading its connections…
          </p>
        )}
        {!initialSetup &&
          (!confirmDestroy ? (
            <button
              type="button"
              className="forgot-password"
              disabled={unlocking}
              onClick={() => setConfirmDestroy(true)}
            >
              <AlertTriangle size={15} />
              Forgot your master password?
            </button>
          ) : (
            <div className="danger-panel">
              <div className="danger-heading">
                <AlertTriangle size={18} />
                <strong>Destroy this vault?</strong>
              </div>
              <p>
                This permanently deletes every saved connection. It cannot be
                undone, and you will need to create a new master password.
              </p>
              <div className="modal-actions">
                <button
                  type="button"
                  className="secondary"
                  disabled={unlocking}
                  onClick={() => setConfirmDestroy(false)}
                >
                  Keep vault
                </button>
                <button
                  type="button"
                  className="danger"
                  onClick={onDestroy}
                  disabled={unlocking}
                >
                  Destroy vault
                </button>
              </div>
            </div>
          ))}
      </form>
    </div>
  );
}

function ConnectionModal({
  connection,
  onCancel,
  onSubmit,
  onTest,
  organizations,
  groups,
}: {
  connection?: Connection;
  onCancel: () => void;
  onSubmit: (value: ConnectionForm) => Promise<void>;
  onTest: (value: ConnectionForm) => Promise<void>;
  organizations: OrganizationOption[];
  groups: ConnectionGroup[];
}) {
  const [form, setForm] = useState<ConnectionForm>({
    id: connection?.id,
    name: connection?.name ?? "",
    bucket: connection?.bucket ?? "",
    region: connection?.region ?? "us-east-1",
    prefix: connection?.prefix ?? "",
    endpoint: connection?.endpoint ?? "",
    accessKey: "",
    secretKey: "",
    organization: connection?.organization ?? "",
    groupPath: connection?.groupPath ?? "",
  });
  const [tested, setTested] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testError, setTestError] = useState("");
  const [fieldErrors, setFieldErrors] = useState<
    Partial<Record<"name" | "bucket" | "region", string>>
  >({});
  const update =
    (field: keyof ConnectionForm) =>
    (event: React.ChangeEvent<HTMLInputElement>) => {
      setForm({ ...form, [field]: event.target.value });
      setTested(false);
      setTestError("");
      if (field in fieldErrors) {
        setFieldErrors((errors) => ({ ...errors, [field]: undefined }));
      }
    };
  function validateForm() {
    const errors: Partial<Record<"name" | "bucket" | "region", string>> = {};
    if (!form.name.trim()) errors.name = "Connection name is required.";
    if (!form.bucket.trim()) errors.bucket = "Bucket name is required.";
    if (!form.region.trim()) errors.region = "Region is required.";
    setFieldErrors(errors);
    return Object.keys(errors).length === 0;
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!validateForm()) return;
    // Existing connections keep their encrypted credentials server-side when
    // the credential fields are blank. The update endpoint merges those
    // credentials and validates the resulting connection, so moving an
    // existing connection does not require re-entering or retesting its keys.
    if (!connection && !tested) return;
    await onSubmit(form);
  }
  async function test() {
    if (!validateForm()) {
      setTestError(
        "Complete the required fields before testing the connection.",
      );
      return;
    }
    setTesting(true);
    setTestError("");
    try {
      await onTest(form);
      setTested(true);
    } catch (err) {
      setTestError(errorMessage(err));
      setTested(false);
    } finally {
      setTesting(false);
    }
  }
  return (
    <div className="modal-backdrop">
      <form className="card connection-modal" onSubmit={submit}>
        <div className="modal-heading">
          <div>
            <span className="eyebrow">
              {connection ? "EDIT CONNECTION" : "NEW CONNECTION"}
            </span>
            <h2>
              {connection ? "Edit S3 connection" : "Add an S3 connection"}
            </h2>
          </div>
          <button
            type="button"
            className="modal-close"
            onClick={onCancel}
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <p className="modal-description">
          {connection
            ? "Update the connection details. Leave credentials blank to keep the current values."
            : "Connection credentials are encrypted in your personal vault."}
        </p>
        <div className="form-grid">
          <label className="full-width">
            Save location
            <span className="form-select-wrap">
              <select
                value={form.organization}
                onChange={(event) => {
                  setForm({ ...form, organization: event.target.value, groupPath: "" });
                  setTested(false);
                  setTestError("");
                }}
              >
                <option value="">Personal vault</option>
                {organizations.map((organization) => (
                  <option key={organization.id} value={organization.id}>
                    {organization.name}
                  </option>
                ))}
              </select>
            </span>
          </label>
          <label className="full-width">
            Group <span>(optional)</span>
            <span className="form-select-wrap">
              <select value={form.groupPath} onChange={(event) => { setForm({ ...form, groupPath: event.target.value }); setTested(false); setTestError(""); }}>
                <option value="">Ungrouped</option>
                {groups.filter((group) => (group.organization ?? "") === form.organization).map((group) => <option key={group.path} value={group.path}>{group.path}</option>)}
              </select>
            </span>
          </label>
          <label>
            Connection name <span className="required">(required)</span>
            <input
              autoFocus
              value={form.name}
              onChange={update("name")}
              placeholder="Production bucket"
              required
            />
            {fieldErrors.name && (
              <small className="field-error" role="alert">
                {fieldErrors.name}
              </small>
            )}
          </label>
          <label>
            Bucket name <span className="required">(required)</span>
            <input
              value={form.bucket}
              onChange={update("bucket")}
              placeholder="my-bucket"
              required
            />
            {fieldErrors.bucket && (
              <small className="field-error" role="alert">
                {fieldErrors.bucket}
              </small>
            )}
          </label>
          <label>
            Region <span className="required">(required)</span>
            <input
              value={form.region}
              onChange={update("region")}
              placeholder="us-east-1"
              required
            />
            {fieldErrors.region && (
              <small className="field-error" role="alert">
                {fieldErrors.region}
              </small>
            )}
          </label>
          <label>
            Prefix <span>(optional)</span>
            <input
              value={form.prefix}
              onChange={update("prefix")}
              placeholder="folder/"
            />
          </label>
          <label className="full-width">
            S3 endpoint <span>(optional)</span>
            <input
              value={form.endpoint}
              onChange={update("endpoint")}
              placeholder="https://s3.example.com"
            />
          </label>
          <label>
            Access key <span>(optional)</span>
            <input
              value={form.accessKey}
              onChange={update("accessKey")}
              autoComplete="off"
            />
          </label>
          <label>
            Secret key <span>(optional)</span>
            <input
              value={form.secretKey}
              onChange={update("secretKey")}
              type="password"
              autoComplete="new-password"
            />
          </label>
        </div>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>
            Cancel
          </button>
          <button
            type="button"
            className="secondary"
            onClick={test}
            disabled={testing}
          >
            {testing && <LoaderCircle className="spin" size={15} />}
            {testing
              ? "Testing…"
              : tested
                ? "Connection tested"
                : "Test connection"}
          </button>
          <button type="submit" className="button" disabled={!connection && !tested}>
            {connection ? "Save changes" : "Save connection"}
          </button>
        </div>
        {testError && (
          <p className="form-error" role="alert">
            {testError}
          </p>
        )}
        {tested && !testError && (
          <p className="test-success" role="status">
            <Check size={15} /> Connection test passed. You can save it now.
          </p>
        )}
      </form>
    </div>
  );
}

function Explorer({
  connection,
  prefix,
  items,
  setActivities,
  uploadControllers,
  onBrowse,
  onRefresh,
  onError,
  hasMore,
  onLoadMore,
  loadingMore,
  itemFilter,
  searchQuery,
  onSearchChange,
  onFilterChange,
}: {
  connection: Connection;
  prefix: string;
  items: Item[];
  setActivities: ActivitySetter;
  uploadControllers: React.MutableRefObject<Map<string, AbortController>>;
  onBrowse: (connection: Connection, prefix?: string) => void;
  onRefresh: () => Promise<void>;
  onError: ErrorHandler;
  hasMore: boolean;
  onLoadMore: () => Promise<void>;
  loadingMore: boolean;
  itemFilter: ItemFilter;
  searchQuery: string;
  onSearchChange: (query: string) => void;
  onFilterChange: (filter: ItemFilter) => void;
}) {
  const [folderOpen, setFolderOpen] = useState(false);
  const [searchInput, setSearchInput] = useState(searchQuery);
  const [dragging, setDragging] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<Item>();
  const [deleteSelectionOpen, setDeleteSelectionOpen] = useState(false);
  const [multipartUploads, setMultipartUploads] = useState<MultipartUpload[]>(
    [],
  );
  const [multipartLoading, setMultipartLoading] = useState(true);
  const [multipartError, setMultipartError] = useState("");
  const [multipartAbortTarget, setMultipartAbortTarget] =
    useState<MultipartUpload>();
  useEffect(() => setSearchInput(searchQuery), [searchQuery]);
  useEffect(() => {
    if (searchInput === searchQuery) return;
    const timer = window.setTimeout(() => onSearchChange(searchInput), 350);
    return () => window.clearTimeout(timer);
  }, [searchInput, searchQuery, onSearchChange]);
  useEffect(() => {
    if (!itemFilter.multipart) {
      setMultipartLoading(false);
      setMultipartError("");
      return;
    }

    const controller = new AbortController();
    let current = true;
    setMultipartLoading(true);
    setMultipartError("");
    api
      .multipartUploads(connection.id, prefix, controller.signal)
      .then(({ uploads }) => {
        if (current) setMultipartUploads(uploads);
      })
      .catch((err) => {
        if (current && !controller.signal.aborted) {
          setMultipartError(errorMessage(err));
        }
      })
      .finally(() => {
        if (current) setMultipartLoading(false);
      });
    return () => {
      current = false;
      controller.abort();
    };
  }, [connection.id, prefix, itemFilter.multipart]);

  async function confirmAbortMultipart() {
    if (!multipartAbortTarget) return;
    try {
      await api.abortMultipart(connection.id, multipartAbortTarget);
      setMultipartUploads((current) =>
        current.filter(
          (item) => item.uploadId !== multipartAbortTarget.uploadId,
        ),
      );
      setMultipartAbortTarget(undefined);
    } catch (err) {
      onError(errorMessage(err));
    }
  }
  const visibleItems = items.filter((item) =>
    item.kind === "file" ? itemFilter.files : itemFilter.folders,
  );
  const visibleMultipartUploads = itemFilter.multipart ? multipartUploads : [];
  const tableRows: BucketTableRow[] = [
    ...visibleItems.map((item) => ({ type: "object" as const, item })),
    ...visibleMultipartUploads.map((upload) => ({
      type: "multipart" as const,
      upload,
    })),
  ].sort((left, right) => {
    const leftName = left.type === "object" ? left.item.name : left.upload.key;
    const rightName =
      right.type === "object" ? right.item.name : right.upload.key;
    return leftName.localeCompare(rightName);
  });
  const breadcrumbSegments = prefix
    .split("/")
    .filter(Boolean)
    .map((name, index, segments) => ({
      name,
      prefix: `${segments.slice(0, index + 1).join("/")}/`,
    }));
  const activityContext = `${connection.name} / ${connection.bucket}`;
  async function downloadArchive(format: "zip" | "tgz") {
    if (Array.from(selected).some((key) => key.startsWith("multipart:")))
      return;
    const id = `${Date.now()}-${format}`;
    const label = `${connection.bucket}${prefix ? `/${prefix}` : ""}.${format}`;
    setActivities((current) => [
      ...current,
      {
        id,
        label,
        context: activityContext,
        kind: "download",
        progress: 35,
        state: "active",
      },
    ]);
    try {
      const blob = await api.download(connection.id, prefix, format);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = label;
      link.click();
      URL.revokeObjectURL(url);
      setActivities((current) =>
        current.map((item) =>
          item.id === id
            ? { ...item, progress: 100, state: "done", finishedAt: Date.now() }
            : item,
        ),
      );
    } catch (err) {
      setActivities((current) =>
        current.map((item) =>
          item.id === id
            ? {
                ...item,
                state: "error",
                error: errorMessage(err),
                finishedAt: Date.now(),
              }
            : item,
        ),
      );
    }
  }
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [selectionAnchor, setSelectionAnchor] = useState<string>();
  const selectableKeys = [
    ...visibleItems.map((item) => item.key),
    ...visibleMultipartUploads.map(multipartSelectionKey),
  ];
  const allVisibleSelected =
    selectableKeys.length > 0 &&
    selectableKeys.every((key) => selected.has(key));

  function toggleSelected(key: string) {
    setSelectionAnchor(key);
    setSelected((current) => {
      const next = new Set(current);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });
  }
  function selectFromRow(key: string, event: React.MouseEvent) {
    if (!event.ctrlKey && !event.metaKey && !event.shiftKey) return false;
    if (event.shiftKey && selectionAnchor) {
      const keys = visibleItems.map((item) => item.key);
      const start = keys.indexOf(selectionAnchor),
        end = keys.indexOf(key);
      if (start >= 0 && end >= 0)
        setSelected(
          (current) =>
            new Set([
              ...current,
              ...keys.slice(Math.min(start, end), Math.max(start, end) + 1),
            ]),
        );
    } else toggleSelected(key);
    return true;
  }
  function selectAll() {
    setSelected((current) => new Set([...current, ...selectableKeys]));
  }
  async function deleteSelected() {
    if (!selected.size) return;
    setDeleteSelectionOpen(true);
  }
  async function confirmDeleteSelected() {
    const pending = Array.from(selected)
      .filter((key) => !key.startsWith("multipart:"))
      .map((key, index) => {
        const item = items.find((candidate) => candidate.key === key);
        return {
          key,
          id: `${Date.now()}-${index}-${key}`,
          label: item?.name || key,
        };
      });
    const selectedMultipart = multipartUploads.filter((upload) =>
      selected.has(multipartSelectionKey(upload)),
    );
    const pendingMultipart = selectedMultipart.map((upload, index) => ({
      upload,
      id: `${Date.now()}-abort-${index}-${upload.uploadId}`,
    }));
    if (!pending.length && !pendingMultipart.length) return;
    setActivities((current) => [
      ...current,
      ...pending.map(({ id, label }) => ({
        id,
        label,
        context: activityContext,
        kind: "delete" as const,
        progress: 0,
        state: "active" as const,
      })),
      ...pendingMultipart.map(({ upload, id }) => ({
        id,
        label: upload.key,
        context: activityContext,
        kind: "delete" as const,
        progress: 0,
        state: "active" as const,
      })),
    ]);
    await Promise.all(
      pending.map(async ({ key, id }) => {
        try {
          await api.deleteFile(connection.id, key);
          setActivities((current) =>
            current.map((activity) =>
              activity.id === id
                ? {
                    ...activity,
                    progress: 100,
                    state: "done",
                    finishedAt: Date.now(),
                  }
                : activity,
            ),
          );
        } catch (err) {
          setActivities((current) =>
            current.map((activity) =>
              activity.id === id
                ? {
                    ...activity,
                    state: "error",
                    error: errorMessage(err),
                    finishedAt: Date.now(),
                  }
                : activity,
            ),
          );
        }
      }),
    );
    await Promise.all(
      pendingMultipart.map(async ({ upload, id }) => {
        try {
          await api.abortMultipart(connection.id, upload);
          setMultipartUploads((current) =>
            current.filter((item) => item.uploadId !== upload.uploadId),
          );
          setActivities((current) =>
            current.map((activity) =>
              activity.id === id
                ? {
                    ...activity,
                    progress: 100,
                    state: "done",
                    finishedAt: Date.now(),
                  }
                : activity,
            ),
          );
        } catch (err) {
          setActivities((current) =>
            current.map((activity) =>
              activity.id === id
                ? {
                    ...activity,
                    state: "error",
                    error: errorMessage(err),
                    finishedAt: Date.now(),
                  }
                : activity,
            ),
          );
        }
      }),
    );
    setSelected(new Set());
    await onRefresh();
  }

  async function uploadFiles(files: FileList | File[]) {
    const pending = Array.from(files).map((file, index) => ({
      file,
      id: `${Date.now()}-${index}-${file.name}`,
      controller: new AbortController(),
    }));
    if (!pending.length) return;
    setActivities((current) => [
      ...current,
      ...pending.map(({ file, id }) => ({
        id,
        label: file.name,
        context: activityContext,
        kind: "upload" as const,
        progress: 0,
        state: "active" as const,
      })),
    ]);
    pending.forEach(({ id, controller }) =>
      uploadControllers.current.set(id, controller),
    );
    await Promise.all(
      pending.map(async ({ file, id, controller }) => {
        try {
          await api.upload(
            connection.id,
            prefix,
            file,
            (progress) =>
              setActivities((current) =>
                current.map((item) =>
                  item.id === id
                    ? {
                        ...item,
                        progress: Math.min(progress, 99),
                        phase: "uploading",
                      }
                    : item,
                ),
              ),
            (phase) =>
              setActivities((current) =>
                current.map((item) =>
                  item.id === id ? { ...item, phase } : item,
                ),
              ),
            controller.signal,
          );
          setActivities((current) =>
            current.map((item) =>
              item.id === id
                ? {
                    ...item,
                    progress: 100,
                    phase: undefined,
                    state: "done",
                    finishedAt: Date.now(),
                  }
                : item,
            ),
          );
        } catch (err) {
          const canceled = controller.signal.aborted;
          setActivities((current) =>
            current.map((item) =>
              item.id === id
                ? {
                    ...item,
                    state: canceled ? "canceled" : "error",
                    error: canceled ? "Canceled" : errorMessage(err),
                    finishedAt: Date.now(),
                  }
                : item,
            ),
          );
        } finally {
          uploadControllers.current.delete(id);
        }
      }),
    );
    await onRefresh();
  }

  async function upload(event: React.ChangeEvent<HTMLInputElement>) {
    if (event.target.files) await uploadFiles(event.target.files);
    event.target.value = "";
  }
  function drop(event: React.DragEvent) {
    event.preventDefault();
    setDragging(false);
    if (event.dataTransfer.files.length) uploadFiles(event.dataTransfer.files);
  }
  return (
    <section
      className={`card explorer ${dragging ? "dragging" : ""}`}
      onDragOver={(event) => {
        event.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={drop}
    >
      <div className="toolbar">
        <nav className="breadcrumbs" aria-label="Directory path">
          <button type="button" onClick={() => onBrowse(connection, "")}>
            {connection.bucket}
          </button>
          {breadcrumbSegments.map((segment) => (
            <span className="breadcrumb-segment" key={segment.prefix}>
              <span aria-hidden="true">/</span>
              <button
                type="button"
                onClick={() => onBrowse(connection, segment.prefix)}
              >
                {segment.name}
              </button>
            </span>
          ))}
        </nav>
        <label className="button upload">
          <Upload size={16} /> Upload
          <input type="file" multiple onChange={upload} />
        </label>
        <button className="secondary" onClick={() => setFolderOpen(true)}>
          <FolderPlus size={16} /> New folder
        </button>
        <DownloadMenu
          onDownload={downloadArchive}
          disabled={Array.from(selected).some((key) =>
            key.startsWith("multipart:"),
          )}
        />
        <button
          type="button"
          className="secondary compact"
          aria-label="Reload current view"
          title="Reload current view"
          onClick={() => void onRefresh()}
        >
          <RefreshCw size={16} />
          <span>Refresh</span>
        </button>
        <button
          className="secondary"
          onClick={
            allVisibleSelected
              ? () =>
                  setSelected(
                    (current) =>
                      new Set(
                        [...current].filter(
                          (key) => !selectableKeys.includes(key),
                        ),
                      ),
                  )
              : selectAll
          }
        >
          {allVisibleSelected ? "Unselect all" : "Select all"}
        </button>
        <button
          className="danger compact"
          disabled={!selected.size}
          onClick={deleteSelected}
        >
          <Trash2 size={16} />
          Delete
          {selected.size ? ` (${selected.size})` : ""}
        </button>
      </div>
      <div className="drop-hint">
        Drop files anywhere in this bucket to upload
      </div>
      <div className="table head">
        <span />
        <span>Name</span>
        <span>Type</span>
        <span>Size</span>
        <span />
      </div>
      <div className="item-filter" aria-label="Filter bucket contents">
        {(itemFilter.files || itemFilter.folders || itemFilter.multipart) && (
          <div className="filter-group" aria-label="Active object filters">
            {itemFilter.files && (
              <button
                type="button"
                className="active"
                aria-pressed="true"
                onClick={() => onFilterChange({ ...itemFilter, files: false })}
              >
                Files
              </button>
            )}
            {itemFilter.folders && (
              <button
                type="button"
                className="active"
                aria-pressed="true"
                onClick={() =>
                  onFilterChange({ ...itemFilter, folders: false })
                }
              >
                Folders
              </button>
            )}
            {itemFilter.multipart && (
              <button
                type="button"
                className="active"
                aria-pressed="true"
                onClick={() =>
                  onFilterChange({ ...itemFilter, multipart: false })
                }
              >
                Multipart
              </button>
            )}
          </div>
        )}
        {!itemFilter.files && (
          <button
            type="button"
            aria-pressed="false"
            onClick={() => onFilterChange({ ...itemFilter, files: true })}
          >
            Files
          </button>
        )}
        {!itemFilter.folders && (
          <button
            type="button"
            aria-pressed="false"
            onClick={() => onFilterChange({ ...itemFilter, folders: true })}
          >
            Folders
          </button>
        )}
        {!itemFilter.multipart && (
          <button
            type="button"
            aria-pressed="false"
            onClick={() => onFilterChange({ ...itemFilter, multipart: true })}
          >
            Multipart
          </button>
        )}
        <label className="bucket-search">
          <span className="sr-only">Search this level</span>
          <input
            type="search"
            value={searchInput}
            placeholder="Search this level"
            onChange={(event) => setSearchInput(event.target.value)}
          />
          {searchInput && (
            <button
              type="button"
              className="bucket-search-clear"
              aria-label="Clear search"
              onClick={() => setSearchInput("")}
            >
              <X size={14} />
            </button>
          )}
        </label>
      </div>
      {multipartError && itemFilter.multipart && (
        <div className="table row table-message form-error" role="alert">
          <span />
          <span>
            Unable to list incomplete multipart uploads: {multipartError}
          </span>
          <span />
          <span />
          <span />
        </div>
      )}
      {!multipartError &&
        tableRows.map((row) => (
          <BucketTableRow
            key={row.type === "object" ? row.item.key : row.upload.uploadId}
            row={row}
            connection={connection}
            onBrowse={onBrowse}
            onRefresh={onRefresh}
            onError={onError}
            onDelete={(item) => setDeleteTarget(item)}
            selected={selected}
            onToggleSelected={toggleSelected}
            onSelectFromRow={selectFromRow}
            onAbort={(upload) => setMultipartAbortTarget(upload)}
          />
        ))}
      {!multipartLoading &&
        !tableRows.length &&
        (items.length > 0 || itemFilter.multipart) && (
          <p className="filtered-empty">
            {itemFilter.multipart && !itemFilter.files && !itemFilter.folders
              ? "No incomplete multipart uploads in this location."
              : "No matching items in this location."}
          </p>
        )}
      {hasMore && (
        <button
          type="button"
          className="secondary load-more"
          onClick={onLoadMore}
          disabled={loadingMore}
        >
          {loadingMore && <LoaderCircle className="spin" size={15} />}
          {loadingMore ? "Loading more…" : "Load more"}
        </button>
      )}
      {folderOpen && (
        <FolderModal
          onCancel={() => setFolderOpen(false)}
          onSubmit={async (name) => {
            await api.createFolder(connection.id, prefix, name);
            setFolderOpen(false);
            onRefresh();
          }}
        />
      )}
      {deleteTarget && (
        <DeleteFileModal
          item={deleteTarget}
          onCancel={() => setDeleteTarget(undefined)}
          onConfirm={async () => {
            const id = `${Date.now()}-${deleteTarget.key}`;
            setDeleteTarget(undefined);
            setActivities((current) => [
              ...current,
              {
                id,
                label: deleteTarget.name,
                context: activityContext,
                kind: "delete",
                progress: 0,
                state: "active",
              },
            ]);
            try {
              await api.deleteFile(connection.id, deleteTarget.key);
              setActivities((current) =>
                current.map((item) =>
                  item.id === id
                    ? {
                        ...item,
                        progress: 100,
                        state: "done",
                        finishedAt: Date.now(),
                      }
                    : item,
                ),
              );
              await onRefresh();
            } catch (err) {
              setActivities((current) =>
                current.map((item) =>
                  item.id === id
                    ? {
                        ...item,
                        state: "error",
                        error: errorMessage(err),
                        finishedAt: Date.now(),
                      }
                    : item,
                ),
              );
            }
          }}
        />
      )}
      {deleteSelectionOpen && (
        <DeleteFileModal
          count={selected.size}
          multipartCount={
            Array.from(selected).filter((key) => key.startsWith("multipart:"))
              .length
          }
          onCancel={() => setDeleteSelectionOpen(false)}
          onConfirm={async () => {
            setDeleteSelectionOpen(false);
            await confirmDeleteSelected();
          }}
        />
      )}
      {multipartAbortTarget && (
        <AbortMultipartModal
          upload={multipartAbortTarget}
          onCancel={() => setMultipartAbortTarget(undefined)}
          onConfirm={confirmAbortMultipart}
        />
      )}
    </section>
  );
}

function DownloadMenu({
  onDownload,
  disabled = false,
}: {
  onDownload: (format: "zip" | "tgz") => void;
  disabled?: boolean;
}) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  function choose(format: "zip" | "tgz") {
    setOpen(false);
    onDownload(format);
  }
  return (
    <div className="download-menu">
      <button
        type="button"
        className="secondary download-trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen((value) => !value)}
      >
        <Download size={16} />
        <span>Download</span>
        <ChevronDown size={16} />
      </button>
      {open && (
        <div className="download-options" role="menu">
          <button type="button" role="menuitem" onClick={() => choose("zip")}>
            Download ZIP
          </button>
          <button type="button" role="menuitem" onClick={() => choose("tgz")}>
            Download TGZ
          </button>
        </div>
      )}
    </div>
  );
}

function FolderModal({
  onCancel,
  onSubmit,
}: {
  onCancel: () => void;
  onSubmit: (name: string) => Promise<void>;
}) {
  const [name, setName] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (name.trim()) await onSubmit(name.trim());
  }
  return (
    <div className="modal-backdrop">
      <form className="card small-modal" onSubmit={submit}>
        <div className="modal-heading">
          <div>
            <span className="eyebrow">NEW FOLDER</span>
            <h2>Create a subfolder</h2>
          </div>
          <button
            type="button"
            className="modal-close"
            onClick={onCancel}
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <p className="modal-description">
          Create a folder inside the currently open bucket path.
        </p>
        <label className="standalone-label">
          Folder name
          <input
            autoFocus
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="documents"
            required
          />
        </label>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>
            Cancel
          </button>
          <button type="submit" className="button">
            Create folder
          </button>
        </div>
      </form>
    </div>
  );
}

function DeleteFileModal({
  item,
  count,
  multipartCount = 0,
  onCancel,
  onConfirm,
}: {
  item?: Item;
  count?: number;
  multipartCount?: number;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  const multiple = !item;
  const hasMultipart = multipartCount > 0;
  return (
    <div className="modal-backdrop">
      <div className="card small-modal">
        <div className="modal-heading">
          <div>
            <span className="eyebrow">
              {hasMultipart ? "DELETE / ABORT" : "DELETE"}{" "}
              {multiple ? "ITEMS" : item?.kind === "folder" ? "FOLDER" : "FILE"}
            </span>
            <h2>
              {multiple
                ? hasMultipart
                  ? `Delete selected items and abort ${multipartCount} upload${multipartCount === 1 ? "" : "s"}?`
                  : `Delete ${count} selected items?`
                : item?.kind === "folder"
                  ? "Delete this folder?"
                  : "Delete this file?"}
            </h2>
          </div>
          <button className="modal-close" onClick={onCancel} aria-label="Close">
            ×
          </button>
        </div>
        <p className="modal-description">
          This will permanently delete{" "}
          {multiple ? (
            hasMultipart ? (
              "the selected files/folders and abort the selected multipart uploads"
            ) : (
              "the selected items"
            )
          ) : (
            <>
              <>
                <strong>{item?.name}</strong>
                {item?.kind === "folder" && " and everything inside it"}
              </>
            </>
          )}{" "}
          from the bucket. This cannot be undone.
        </p>
        <div className="modal-actions">
          <button className="secondary" onClick={onCancel}>
            Cancel
          </button>
          <button className="danger" onClick={onConfirm}>
            {hasMultipart ? "Delete / Abort" : "Delete"}{" "}
            {multiple ? "items" : item?.kind === "folder" ? "folder" : "file"}
          </button>
        </div>
      </div>
    </div>
  );
}

function ConnectionGroupModal({ organizations, onCancel, onSubmit }: { organizations: OrganizationOption[]; onCancel: () => void; onSubmit: (organization: string, path: string) => Promise<void> }) {
  const [organization, setOrganization] = useState("");
  const [path, setPath] = useState("");
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!path.trim()) { setError("Group path is required."); return; }
    await onSubmit(organization, path.trim());
  }
  return <div className="modal-backdrop"><form className="card small-modal" onSubmit={submit}><div className="modal-heading"><div><span className="eyebrow">NEW GROUP</span><h2>Create connection group</h2></div><button type="button" className="modal-close" onClick={onCancel} aria-label="Close">×</button></div><p className="modal-description">Use slash-separated names to create nested groups, up to five levels deep.</p><div className="form-grid"><label className="full-width">Location<span className="form-select-wrap"><select value={organization} onChange={(event) => setOrganization(event.target.value)}><option value="">Personal vault</option>{organizations.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></span></label><label className="full-width">Group path<input autoFocus value={path} onChange={(event) => setPath(event.target.value)} placeholder="Engineering/Production" /></label></div><div className="modal-actions"><button type="button" className="secondary" onClick={onCancel}>Cancel</button><button type="submit" className="button">Create group</button></div>{error && <p className="form-error" role="alert">{error}</p>}</form></div>;
}

function RenameConnectionGroupModal({ group, onCancel, onSubmit }: { group: ConnectionGroup; onCancel: () => void; onSubmit: (newPath: string) => Promise<void> }) {
  const [path, setPath] = useState(group.path);
  const [error, setError] = useState("");
  async function submit(event: FormEvent) {
    event.preventDefault();
    const next = path.trim();
    if (!next) { setError("Group path is required."); return; }
    try { await onSubmit(next); } catch (err) { setError(errorMessage(err)); }
  }
  return <div className="modal-backdrop"><form className="card small-modal" onSubmit={submit}><div className="modal-heading"><div><span className="eyebrow">EDIT GROUP</span><h2>Rename connection group</h2></div><button type="button" className="modal-close" onClick={onCancel} aria-label="Close">×</button></div><p className="modal-description">Rename this group and keep its connections and nested groups together.</p><div className="form-grid"><label className="full-width">Group path<input autoFocus value={path} onChange={(event) => setPath(event.target.value)} /></label></div><div className="modal-actions"><button type="button" className="secondary" onClick={onCancel}>Cancel</button><button type="submit" className="button">Save name</button></div>{error && <p className="form-error" role="alert">{error}</p>}</form></div>;
}

function DeleteConnectionModal({
  connection,
  onCancel,
  onConfirm,
}: {
  connection: Connection;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <div className="modal-backdrop">
      <div className="card small-modal">
        <div className="modal-heading">
          <div>
            <span className="eyebrow">DELETE CONNECTION</span>
            <h2>Delete this connection?</h2>
          </div>
          <button
            type="button"
            className="modal-close"
            onClick={onCancel}
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <div className="modal-warning">
          <AlertTriangle size={20} aria-hidden="true" />
          <p>
            This will remove <strong>{connection.name}</strong> and its saved
            credentials from your vault. Objects in the S3 bucket will not be
            deleted.
          </p>
        </div>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>
            Keep connection
          </button>
          <button type="button" className="danger" onClick={onConfirm}>
            <Trash2 size={15} /> Delete connection
          </button>
        </div>
      </div>
    </div>
  );
}

function AbortMultipartModal({
  upload,
  onCancel,
  onConfirm,
}: {
  upload: MultipartUpload;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <div className="modal-backdrop">
      <div className="card small-modal">
        <div className="modal-heading">
          <div>
            <span className="eyebrow">INCOMPLETE MULTIPART UPLOAD</span>
            <h2>Abort this upload?</h2>
          </div>
          <button
            type="button"
            className="modal-close"
            onClick={onCancel}
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <div className="modal-warning">
          <AlertTriangle size={20} aria-hidden="true" />
          <p>
            This will permanently delete the incomplete S3 parts for{" "}
            <strong>{upload.key}</strong>. No completed object will be deleted.
          </p>
        </div>
        <p className="modal-description">
          Started {new Date(upload.initiated).toLocaleString()}.
        </p>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>
            Keep upload
          </button>
          <button type="button" className="danger" onClick={onConfirm}>
            <Trash2 size={15} /> Abort upload
          </button>
        </div>
      </div>
    </div>
  );
}

function ActivityTray({
  activities,
  onCancel,
  onDismiss,
}: {
  activities: Activity[];
  onCancel: (id: string) => void;
  onDismiss: (id: string) => void;
}) {
  const [minimized, setMinimized] = useState(false);
  if (!activities.length) return null;
  const clear = () => {
    activities
      .filter((activity) => activity.state !== "active")
      .forEach((activity) => onDismiss(activity.id));
  };
  return (
    <div className={`activity-tray ${minimized ? "minimized" : ""}`}>
      <div className="activity-header">
        <strong>Activity</strong>
        <div className="activity-header-actions">
          {activities.some((activity) => activity.state !== "active") && (
            <button onClick={clear}>Clear</button>
          )}
          <button onClick={() => setMinimized((value) => !value)}>
            {minimized ? "Show" : "Minimize"}
          </button>
        </div>
      </div>
      {!minimized && (
        <div className="activity-list">
          {activities.map((activity) => (
            <div className="activity" key={activity.id}>
              <div className="activity-top">
                <span>
                  {activity.kind === "upload"
                    ? "Uploading"
                    : activity.kind === "delete"
                      ? "Deleting"
                      : "Downloading"}{" "}
                  {activity.label}
                </span>
                {activity.state !== "active" && (
                  <button
                    className="activity-dismiss"
                    onClick={() => onDismiss(activity.id)}
                  >
                    ×
                  </button>
                )}
                {activity.state === "active" && activity.kind === "upload" && (
                  <button
                    className="activity-dismiss"
                    onClick={() => onCancel(activity.id)}
                    aria-label={`Cancel upload ${activity.label}`}
                  >
                    ×
                  </button>
                )}
              </div>
              <div className="activity-progress">
                <span
                  className={`${activity.state} ${activity.kind !== "upload" && activity.state === "active" ? "indeterminate" : ""}`}
                  style={{ width: `${activity.progress}%` }}
                />
              </div>
              <small className="activity-context">{activity.context}</small>
              <small>
                {activity.state === "active" ? (
                  activity.kind === "upload" ? (
                    activity.phase === "finalizing" ? (
                      "Finalizing upload…"
                    ) : (
                      `${activity.progress}%`
                    )
                  ) : (
                    "Working…"
                  )
                ) : activity.state === "done" ? (
                  <>
                    <Check size={13} /> Complete
                  </>
                ) : activity.state === "canceled" ? (
                  "Canceled"
                ) : (
                  activity.error || "Failed"
                )}
              </small>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

type BucketTableRow =
  | { type: "object"; item: Item }
  | { type: "multipart"; upload: MultipartUpload };

function BucketTableRow({
  row,
  connection,
  onBrowse,
  onRefresh,
  onError,
  onDelete,
  selected,
  onToggleSelected,
  onSelectFromRow,
  onAbort,
}: {
  row: BucketTableRow;
  connection: Connection;
  onBrowse: (connection: Connection, prefix?: string) => void;
  onRefresh: () => Promise<void>;
  onError: ErrorHandler;
  onDelete: (item: Item) => void;
  selected: Set<string>;
  onToggleSelected: (key: string) => void;
  onSelectFromRow: (key: string, event: React.MouseEvent) => boolean;
  onAbort: (upload: MultipartUpload) => void;
}) {
  if (row.type === "multipart") {
    return (
      <MultipartUploadRow
        upload={row.upload}
        selected={selected.has(multipartSelectionKey(row.upload))}
        onToggleSelected={() =>
          onToggleSelected(multipartSelectionKey(row.upload))
        }
        onAbort={() => onAbort(row.upload)}
      />
    );
  }
  return (
    <ExplorerRow
      item={row.item}
      connection={connection}
      onBrowse={onBrowse}
      onRefresh={onRefresh}
      onError={onError}
      onDelete={() => onDelete(row.item)}
      selected={selected.has(row.item.key)}
      onToggleSelected={onToggleSelected}
      onSelectFromRow={onSelectFromRow}
    />
  );
}

function MultipartUploadRow({
  upload,
  selected,
  onToggleSelected,
  onAbort,
}: {
  upload: MultipartUpload;
  selected: boolean;
  onToggleSelected: () => void;
  onAbort: () => void;
}) {
  return (
    <div className="table row multipart-upload-row">
      <input
        type="checkbox"
        checked={selected}
        onChange={onToggleSelected}
        aria-label={`Select incomplete multipart upload ${upload.key}`}
      />
      <span className="multipart-upload-name">
        <span>
          <Upload size={18} /> {upload.key}
        </span>
      </span>
      <span>Multipart upload</span>
      <span>Incomplete</span>
      <span className="row-actions">
        <RowActionMenu
          label={`Actions for incomplete multipart upload ${upload.key}`}
          actions={[{ label: "Abort upload", danger: true, onSelect: onAbort }]}
        />
      </span>
    </div>
  );
}

type RowAction = {
  label: string;
  danger?: boolean;
  onSelect: () => void | Promise<void>;
};

function RowActionMenu({
  label,
  actions,
}: {
  label: string;
  actions: RowAction[];
}) {
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLSpanElement>(null);

  useEffect(() => {
    const close = () => setOpen(false);
    const onPointerDown = (event: PointerEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) close();
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("mariner:close-row-menus", close);
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("mariner:close-row-menus", close);
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  function toggle(event: React.MouseEvent) {
    event.stopPropagation();
    if (!open) window.dispatchEvent(new Event("mariner:close-row-menus"));
    setOpen((current) => !current);
  }

  return (
    <span className="row-menu" ref={menuRef}>
      <button
        type="button"
        className="icon row-menu-trigger"
        onClick={toggle}
        aria-label={label}
        aria-expanded={open}
        title="More actions"
      >
        <MoreVertical size={18} />
      </button>
      {open && (
        <div className="row-menu-options" role="menu">
          {actions.map((action) => (
            <button
              type="button"
              key={action.label}
              className={action.danger ? "danger-action" : ""}
              role="menuitem"
              onClick={(event) => {
                event.stopPropagation();
                setOpen(false);
                void action.onSelect();
              }}
            >
              {action.label}
            </button>
          ))}
        </div>
      )}
    </span>
  );
}

function ExplorerRow({
  item,
  connection,
  onBrowse,
  onRefresh,
  onError,
  onDelete,
  selected,
  onToggleSelected,
  onSelectFromRow,
}: {
  item: Item;
  connection: Connection;
  onBrowse: (connection: Connection, prefix?: string) => void;
  onRefresh: () => void;
  onError: ErrorHandler;
  onDelete: () => void;
  selected: boolean;
  onToggleSelected: (key: string) => void;
  onSelectFromRow: (key: string, event: React.MouseEvent) => boolean;
}) {
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [versions, setVersions] = useState<ObjectVersion[]>([]);
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [versionsError, setVersionsError] = useState("");
  const [deleteVersionTarget, setDeleteVersionTarget] =
    useState<ObjectVersion>();

  async function toggleVersions() {
    if (versionsOpen) {
      setVersionsOpen(false);
      return;
    }
    setVersionsOpen(true);
    setVersionsLoading(true);
    setVersionsError("");
    try {
      const result = await api.versions(connection.id, item.key);
      setVersions(result.versions);
    } catch (err) {
      setVersionsError(errorMessage(err));
    } finally {
      setVersionsLoading(false);
    }
  }

  async function confirmDeleteVersion() {
    if (!deleteVersionTarget) return;
    try {
      await api.deleteVersion(connection.id, deleteVersionTarget);
      setVersions((current) =>
        current.filter(
          (version) => version.versionId !== deleteVersionTarget.versionId,
        ),
      );
      setDeleteVersionTarget(undefined);
      onRefresh();
    } catch (err) {
      onError(errorMessage(err));
    }
  }

  function remove() {
    onDelete();
  }
  const rowActions: RowAction[] = [];
  if (item.kind === "folder") {
    rowActions.push({
      label: "Open folder",
      onSelect: () => onBrowse(connection, item.key),
    });
  }
  if (item.kind === "file") {
    rowActions.push(
      {
        label: "Download latest",
        onSelect: () => {
          window.open(api.fileUrl(connection.id, item.key), "_blank");
        },
      },
      {
        label: versionsOpen ? "Hide versions" : "Show versions",
        onSelect: toggleVersions,
      },
    );
  }
  rowActions.push({
    label: item.kind === "folder" ? "Delete folder" : "Delete file",
    danger: true,
    onSelect: remove,
  });

  function versionActions(version: ObjectVersion): RowAction[] {
    const actions: RowAction[] = [];
    if (!version.deleteMarker) {
      actions.push({
        label: "Download version",
        onSelect: () => {
          window.open(
            api.fileUrl(connection.id, version.key, version.versionId),
            "_blank",
          );
        },
      });
    }
    actions.push({
      label: "Delete version",
      danger: true,
      onSelect: () => setDeleteVersionTarget(version),
    });
    return actions;
  }

  return (
    <>
      <div
        className="table row"
        onClick={(event) => {
          if (onSelectFromRow(item.key, event)) return;
          onToggleSelected(item.key);
        }}
        onDoubleClick={() => {
          if (item.kind === "folder") onBrowse(connection, item.key);
        }}
      >
        <input
          type="checkbox"
          checked={selected}
          onChange={() => onToggleSelected(item.key)}
          onClick={(event) => event.stopPropagation()}
          aria-label={`Select ${item.name}`}
        />
        <span>
          {item.kind === "folder" ? <Folder size={18} /> : <File size={18} />}{" "}
          {item.name}
        </span>
        <span>{item.kind}</span>
        <span
          className="byte-size"
          aria-label={item.kind === "file" ? exactBytes(item.size ?? 0) : undefined}
          data-tooltip={
            item.kind === "file" ? exactBytes(item.size ?? 0) : undefined
          }
          tabIndex={item.kind === "file" ? 0 : undefined}
        >
          {item.kind === "file" ? formatBytes(item.size ?? 0) : "—"}
        </span>
        <span className="row-actions">
          <RowActionMenu
            label={`Actions for ${item.name}`}
            actions={rowActions}
          />
        </span>
      </div>
      {item.kind === "file" && versionsOpen && (
        <div className="version-subtable">
          {versionsLoading ? (
            <small>Loading object versions…</small>
          ) : versionsError ? (
            <p className="form-error" role="alert">
              Unable to load versions: {versionsError}
            </p>
          ) : versions.length ? (
            <>
              <div className="version-subtable-head">
                <span>Version</span>
                <span>Date</span>
                <span>Size</span>
                <span>Status</span>
                <span />
              </div>
              {versions.map((version) => (
                <div className="version-row" key={version.versionId}>
                  <code title={version.versionId}>
                    {version.versionId === "null"
                      ? "Unversioned"
                      : version.versionId}
                  </code>
                  <span>{new Date(version.lastModified).toLocaleString()}</span>
                  <span
                    className="byte-size"
                    aria-label={
                      version.deleteMarker ? undefined : exactBytes(version.size)
                    }
                    data-tooltip={
                      version.deleteMarker ? undefined : exactBytes(version.size)
                    }
                    tabIndex={version.deleteMarker ? undefined : 0}
                  >
                    {version.deleteMarker ? "—" : formatBytes(version.size)}
                  </span>
                  <span>
                    {version.deleteMarker
                      ? "Delete marker"
                      : version.versionId === "null"
                        ? "Unversioned"
                        : version.isLatest
                          ? "Latest"
                          : "Previous"}
                  </span>
                  <RowActionMenu
                    label={`Actions for version of ${version.key}`}
                    actions={versionActions(version)}
                  />
                </div>
              ))}
            </>
          ) : (
            <small>No versions found for this object.</small>
          )}
        </div>
      )}
      {deleteVersionTarget && (
        <DeleteVersionModal
          version={deleteVersionTarget}
          onCancel={() => setDeleteVersionTarget(undefined)}
          onConfirm={confirmDeleteVersion}
        />
      )}
    </>
  );
}

function DeleteVersionModal({
  version,
  onCancel,
  onConfirm,
}: {
  version: ObjectVersion;
  onCancel: () => void;
  onConfirm: () => Promise<void>;
}) {
  return (
    <div className="modal-backdrop">
      <div className="card small-modal">
        <div className="modal-heading">
          <div>
            <span className="eyebrow">DELETE OBJECT VERSION</span>
            <h2>Delete this version?</h2>
          </div>
          <button
            type="button"
            className="modal-close"
            onClick={onCancel}
            aria-label="Close"
          >
            ×
          </button>
        </div>
        <div className="modal-warning">
          <AlertTriangle size={20} aria-hidden="true" />
          <p>
            This permanently deletes one version of{" "}
            <strong>{version.key}</strong>. This cannot be undone.
          </p>
        </div>
        <p className="modal-description">
          {version.deleteMarker
            ? "Delete marker"
            : version.isLatest
              ? "Latest version"
              : "Previous version"}{" "}
          from {new Date(version.lastModified).toLocaleString()}.
        </p>
        <div className="modal-actions">
          <button type="button" className="secondary" onClick={onCancel}>
            Keep version
          </button>
          <button type="button" className="danger" onClick={onConfirm}>
            <Trash2 size={15} /> Delete version
          </button>
        </div>
      </div>
    </div>
  );
}
