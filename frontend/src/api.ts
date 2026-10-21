import { csrfToken } from "./utils/csrf";

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
export type BrowseKind = "all" | "file" | "folder" | "multipart";
export type MultipartUpload = {
  uploadId: string;
  key: string;
  initiated: string;
};
export type ObjectVersion = {
  versionId: string;
  key: string;
  isLatest: boolean;
  lastModified: string;
  size: number;
  etag?: string;
  deleteMarker: boolean;
};
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
export type OrganizationConnection = Connection & {
  accessKey?: string;
  secretKey?: string;
};
export type Organization = {
  id: string;
  name: string;
  groups: string[];
  provisioned: boolean;
  connections: Record<string, OrganizationConnection>;
};
export type ApplicationSettings = { archiveMaxBytes: number };

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const method = (init?.method ?? "GET").toUpperCase();
  const headers = new Headers(init?.headers);
  if (!["GET", "HEAD", "OPTIONS"].includes(method)) {
    const token = csrfToken();
    if (token) headers.set("X-CSRF-Token", token);
  }
  const response = await fetch(url, { ...init, headers });
  const body = response.ok ? "" : await response.text();

  if (!response.ok) {
    const needsLogin =
      url !== "/api/vault/unlock" &&
      ((response.status === 401 && body.includes("sign in required")) ||
        (response.status === 423 && body.includes("sign in required")));
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
    const params = new URLSearchParams({
      ...filters,
      offset: String(offset),
      limit: "50",
    });
    return request<{
      events: AuditEvent[];
      nextOffset: number;
      hasMore: boolean;
    }>(`/api/admin/audit?${params}`);
  },
  auditActions: () => request<string[]>("/api/admin/audit/actions"),
  adminPreferences: () =>
    request<Record<string, boolean>>("/api/admin/preferences"),
  adminSettings: () => request<ApplicationSettings>("/api/admin/settings"),
  updateAdminSettings: (settings: ApplicationSettings) =>
    request<ApplicationSettings>("/api/admin/settings", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(settings),
    }),
  updateAdminPreferences: (preferences: Record<string, boolean>) =>
    request<Record<string, boolean>>("/api/admin/preferences", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(preferences),
    }),
  adminOrganizations: () => request<Organization[]>("/api/admin/organizations"),
  createOrganization: (organization: Organization) =>
    request<Organization>("/api/admin/organizations", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(organization),
    }),
  updateOrganization: (organization: Organization) =>
    request<Organization>("/api/admin/organizations", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(organization),
    }),
  deleteOrganization: (id: string) =>
    request<void>(`/api/admin/organizations?id=${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  logout: () =>
    fetch("/auth/logout", {
      method: "POST",
      headers: { "X-CSRF-Token": csrfToken() },
    }),
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
      `/api/browse?connection=${encodeURIComponent(id)}&prefix=${encodeURIComponent(prefix)}&kind=${kind}${nextToken ? `&continuationToken=${encodeURIComponent(nextToken)}` : ""}`,
    ),
  search: (
    id: string,
    prefix: string,
    query: string,
    kind: BrowseKind = "all",
  ) =>
    request<BrowseResponse>(
      `/api/search?connection=${encodeURIComponent(id)}&prefix=${encodeURIComponent(prefix)}&query=${encodeURIComponent(query)}&kind=${kind}`,
    ),
  multipartUploads: (id: string, prefix = "", signal?: AbortSignal) =>
    request<{ uploads: MultipartUpload[] }>(
      `/api/multipart?connection=${encodeURIComponent(id)}&prefix=${encodeURIComponent(prefix)}`,
      { signal },
    ),
  abortMultipart: (id: string, upload: MultipartUpload) =>
    request<void>(
      `/api/multipart?connection=${encodeURIComponent(id)}&uploadId=${encodeURIComponent(upload.uploadId)}&key=${encodeURIComponent(upload.key)}`,
      { method: "DELETE" },
    ),
  versions: (id: string, key: string) =>
    request<{ versions: ObjectVersion[] }>(
      `/api/versions?connection=${encodeURIComponent(id)}&key=${encodeURIComponent(key)}`,
    ),
  deleteVersion: (id: string, version: ObjectVersion) =>
    request<void>(
      `/api/versions?connection=${encodeURIComponent(id)}&key=${encodeURIComponent(version.key)}&versionId=${encodeURIComponent(version.versionId)}`,
      { method: "DELETE" },
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
    request<void>(
      `/api/file?connection=${encodeURIComponent(id)}&key=${encodeURIComponent(key)}`,
      {
        method: "DELETE",
      },
    ),
  fileUrl: (id: string, key: string, versionId?: string) =>
    `/api/file?connection=${encodeURIComponent(id)}&key=${encodeURIComponent(key)}${versionId ? `&versionId=${encodeURIComponent(versionId)}` : ""}`,
  download: (id: string, prefix: string, format: "zip" | "tgz") =>
    fetch(
      `/api/download?connection=${encodeURIComponent(id)}&prefix=${encodeURIComponent(prefix)}&format=${format}`,
    ).then(async (response) => {
      if (!response.ok) throw new Error(await response.text());
      return response.blob();
    }),
  upload: (
    id: string,
    prefix: string,
    file: File,
    onProgress?: (value: number) => void,
    onPhase?: (phase: "uploading" | "finalizing") => void,
    signal?: AbortSignal,
  ) =>
    (async () => {
      if (file.size > 32 * 1024 * 1024) {
        const { uploadId, key, partSize, concurrency } = await request<{
          uploadId: string;
          key: string;
          partSize: number;
          concurrency: number;
        }>("/api/upload/init", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            connection: id,
            prefix,
            name: file.name,
            size: file.size,
            contentType: file.type,
          }),
        });
        const configuredPartSize = partSize > 0 ? partSize : 16 * 1024 * 1024;
        const configuredConcurrency = concurrency > 0 ? concurrency : 1;
        try {
          let uploadedBytes = 0;
          let offset = 0;
          let partNumber = 1;
          while (offset < file.size) {
            const batch: Array<{ partNumber: number; chunk: Blob }> = [];
            while (batch.length < configuredConcurrency && offset < file.size) {
              const chunk = file.slice(
                offset,
                Math.min(offset + configuredPartSize, file.size),
              );
              batch.push({ partNumber, chunk });
              offset += chunk.size;
              partNumber += 1;
            }
            const batchProgress = new Map<number, number>();
            await Promise.all(
              batch.map(
                ({ partNumber: batchPartNumber, chunk }) =>
                  new Promise<void>((resolve, reject) => {
                    const xhr = new XMLHttpRequest();
                    const abort = () => xhr.abort();
                    signal?.addEventListener("abort", abort, { once: true });
                    xhr.open(
                      "PUT",
                      `/api/upload/part?connection=${encodeURIComponent(id)}&uploadId=${encodeURIComponent(uploadId)}&partNumber=${batchPartNumber}`,
                    );
                    const token = csrfToken();
                    if (token) xhr.setRequestHeader("X-CSRF-Token", token);
                    xhr.upload.onprogress = (event) => {
                      if (event.lengthComputable) {
                        batchProgress.set(batchPartNumber, event.loaded);
                        const uploaded =
                          uploadedBytes +
                          [...batchProgress.values()].reduce(
                            (total, value) => total + value,
                            0,
                          );
                        onProgress?.(Math.round((uploaded / file.size) * 100));
                      }
                    };
                    xhr.onload = () => {
                      signal?.removeEventListener("abort", abort);
                      if (xhr.status >= 200 && xhr.status < 300) {
                        uploadedBytes += chunk.size;
                        resolve();
                      } else
                        reject(
                          new Error(xhr.responseText || "Upload part failed"),
                        );
                    };
                    xhr.onerror = () => {
                      signal?.removeEventListener("abort", abort);
                      reject(new Error("Upload part failed"));
                    };
                    xhr.onabort = () => {
                      signal?.removeEventListener("abort", abort);
                      reject(new DOMException("Upload canceled", "AbortError"));
                    };
                    xhr.send(chunk);
                  }),
              ),
            );
          }
          onPhase?.("finalizing");
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
        return;
      }
      await new Promise<void>((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        const abort = () => xhr.abort();
        signal?.addEventListener("abort", abort, { once: true });
        xhr.open(
          "POST",
          `/api/upload?connection=${encodeURIComponent(id)}&prefix=${encodeURIComponent(prefix)}`,
        );
        const token = csrfToken();
        if (token) xhr.setRequestHeader("X-CSRF-Token", token);
        xhr.upload.onprogress = (event) => {
          if (event.lengthComputable) {
            onProgress?.(
              Math.round((event.loaded / Math.max(event.total, 1)) * 100),
            );
          }
        };
        xhr.onload = () => {
          signal?.removeEventListener("abort", abort);
          if (xhr.status >= 200 && xhr.status < 300) resolve();
          else reject(new Error(xhr.responseText || "Upload failed"));
        };
        xhr.onerror = () => {
          signal?.removeEventListener("abort", abort);
          reject(new Error("Upload failed"));
        };
        xhr.onabort = () => {
          signal?.removeEventListener("abort", abort);
          reject(new DOMException("Upload canceled", "AbortError"));
        };
        const body = new FormData();
        body.append("file", file, file.name);
        xhr.send(body);
      });
      onProgress?.(100);
    })(),
};
