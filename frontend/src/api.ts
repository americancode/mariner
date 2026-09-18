export type Connection = {
  id: string;
  name: string;
  bucket: string;
  region: string;
  prefix: string;
  endpoint?: string;
};

export type Item = {
  name: string;
  key: string;
  kind: "file" | "folder";
  size?: number;
  modified?: string;
};
export type BrowseResponse = {
  items: Item[];
  nextToken?: string;
  hasMore: boolean;
};
export type BrowseKind = "all" | "file" | "folder";
export type Settings = { theme?: "light" | "dark" };
export type AuditEvent = {
  event_id: string;
  occurred_at: string;
  action: string;
  result: string;
  user_id?: string;
  user_name?: string;
  organization?: string;
  connection_id?: string;
  bucket?: string;
  object_key?: string;
  file_sha256?: string;
  error?: string;
};
export type OrganizationConnection = Connection & { accessKey?: string; secretKey?: string };
export type Organization = {
  id: string;
  name: string;
  groups: string[];
  provisioned: boolean;
  connections: Record<string, OrganizationConnection>;
};

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, init);
  const body = response.ok ? "" : await response.text();

  if (!response.ok) {
    const needsLogin =
      response.status === 401 ||
      (response.status === 423 && body.includes("sign in required"));
    if (needsLogin) {
      window.location.assign(
        `/auth/login?return=${encodeURIComponent(window.location.pathname + window.location.search)}`,
      );
    }
    throw new Error(body);
  }

  return response.headers.get("content-type")?.includes("json")
    ? response.json()
    : (undefined as T);
}

export const api = {
  me: () =>
    request<{ authenticated: boolean; name?: string; isAdmin?: boolean }>(
      "/api/me",
    ),
  audit: (filters: Record<string, string>, offset = 0) => {
    const params = new URLSearchParams({ ...filters, offset: String(offset), limit: "50" });
    return request<{ events: AuditEvent[]; nextOffset: number; hasMore: boolean }>(
      `/api/admin/audit?${params}`,
    );
  },
  auditActions: () => request<string[]>("/api/admin/audit/actions"),
  adminPreferences: () => request<Record<string, boolean>>("/api/admin/preferences"),
  updateAdminPreferences: (preferences: Record<string, boolean>) => request<Record<string, boolean>>("/api/admin/preferences", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(preferences) }),
  adminOrganizations: () => request<Organization[]>("/api/admin/organizations"),
  createOrganization: (organization: Organization) => request<Organization>("/api/admin/organizations", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(organization) }),
  updateOrganization: (organization: Organization) => request<Organization>("/api/admin/organizations", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(organization) }),
  deleteOrganization: (id: string) => request<void>(`/api/admin/organizations?id=${encodeURIComponent(id)}`, { method: "DELETE" }),
  status: () => request<{ exists: boolean }>("/api/vault/status"),
  unlock: async (password: string) => {
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 60_000);
    try {
      return await request<Connection[]>("/api/vault/unlock", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password }),
        signal: controller.signal,
      });
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") {
        throw new Error(
          "Unlock timed out. Check the connection and try again.",
        );
      }
      throw error;
    } finally {
      window.clearTimeout(timeout);
    }
  },
  lock: () => request<void>("/api/vault/lock", { method: "POST" }),
  destroyVault: () => request<void>("/api/vault", { method: "DELETE" }),
  settings: () => request<Settings>("/api/settings"),
  updateSettings: (settings: Settings) =>
    request<Settings>("/api/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    }),
  browse: (
    id: string,
    prefix: string,
    kind: BrowseKind = "all",
    nextToken = "",
  ) =>
    request<BrowseResponse>(
      `/api/browse?connection=${id}&prefix=${encodeURIComponent(prefix)}&kind=${kind}${nextToken ? `&continuationToken=${encodeURIComponent(nextToken)}` : ""}`,
    ),
  connections: () => request<Connection[]>("/api/connections"),
  addConnection: (value: object) =>
    request<{ id: string }>("/api/connections", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(value),
    }),
  testConnection: (value: object) =>
    request<void>("/api/connections/test", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(value),
    }),
  updateConnection: (value: object) =>
    request<void>("/api/connections", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(value),
    }),
  deleteConnection: (id: string) =>
    request<void>(`/api/connections?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  createFolder: (id: string, prefix: string, name: string) =>
    request<{ key: string }>(
      "/api/folders?connection=" + encodeURIComponent(id),
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ prefix, name }),
      },
    ),
  deleteFile: (id: string, key: string) =>
    request<void>(`/api/file?connection=${id}&key=${encodeURIComponent(key)}`, {
      method: "DELETE",
    }),
  download: (id: string, prefix: string, format: "zip" | "tgz") =>
    fetch(
      `/api/download?connection=${id}&prefix=${encodeURIComponent(prefix)}&format=${format}`,
    ).then(async (response) => {
      if (!response.ok) throw new Error(await response.text());
      return response.blob();
    }),
  upload: (
    id: string,
    prefix: string,
    file: File,
    onProgress?: (value: number) => void,
  ) =>
    (async () => {
      const { uploadId, key } = await request<{ uploadId: string; key: string }>(
        "/api/upload/init",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            connection: id,
            prefix,
            name: file.name,
            size: file.size,
            contentType: file.type,
          }),
        },
      );
      const partSize = 16 * 1024 * 1024;
      try {
        let partNumber = 1;
        for (let offset = 0; offset < file.size || (file.size === 0 && partNumber === 1); offset += partSize) {
          const chunk = file.slice(offset, Math.min(offset + partSize, file.size));
          await new Promise<void>((resolve, reject) => {
            const xhr = new XMLHttpRequest();
            xhr.open(
              "PUT",
              `/api/upload/part?connection=${encodeURIComponent(id)}&uploadId=${encodeURIComponent(uploadId)}&partNumber=${partNumber}`,
            );
            xhr.upload.onprogress = (event) => {
              if (event.lengthComputable) {
                const uploaded = offset + event.loaded;
                onProgress?.(Math.round((uploaded / Math.max(file.size, 1)) * 100));
              }
            };
            xhr.onload = () => {
              if (xhr.status >= 200 && xhr.status < 300) resolve();
              else reject(new Error(xhr.responseText || "Upload part failed"));
            };
            xhr.onerror = () => reject(new Error("Upload part failed"));
            xhr.send(chunk);
          });
          partNumber += 1;
        }
        await request<void>(
          `/api/upload/complete?connection=${encodeURIComponent(id)}&uploadId=${encodeURIComponent(uploadId)}&key=${encodeURIComponent(key)}`,
          { method: "POST" },
        );
        onProgress?.(100);
      } catch (error) {
        await request<void>(
          `/api/upload?uploadId=${encodeURIComponent(uploadId)}`,
          { method: "DELETE" },
        ).catch(() => undefined);
        throw error;
      }
    })(),
};
