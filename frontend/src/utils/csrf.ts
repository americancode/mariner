export function csrfToken() {
  const token = document.cookie
    .split("; ")
    .find((entry) => entry.startsWith("mariner_csrf="))
    ?.slice("mariner_csrf=".length);
  return token ? decodeURIComponent(token) : "";
}
