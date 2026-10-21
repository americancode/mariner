import { useEffect, useState } from "react";

const themeStorageKey = "mariner.theme";
export type Theme = "light" | "dark";

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(() =>
    window.localStorage.getItem(themeStorageKey) === "dark" ? "dark" : "light",
  );

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  function setTheme(nextTheme: Theme) {
    setThemeState(nextTheme);
    window.localStorage.setItem(themeStorageKey, nextTheme);
  }

  return { theme, darkMode: theme === "dark", setTheme };
}
