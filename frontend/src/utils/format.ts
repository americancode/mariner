const byteUnits = ["B", "KB", "MB", "GB", "TB", "PB"];

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes === 0) return "0 B";

  const unitIndex = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    byteUnits.length - 1,
  );
  const value = bytes / 1024 ** unitIndex;
  const maximumFractionDigits = unitIndex === 0 ? 0 : value >= 100 ? 0 : 1;
  return `${value.toLocaleString(undefined, { maximumFractionDigits })} ${byteUnits[unitIndex]}`;
}

export function exactBytes(bytes: number): string {
  return `${Math.max(0, Math.trunc(bytes)).toLocaleString()} bytes`;
}
