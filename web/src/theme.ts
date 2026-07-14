// Client-side theme: an explicit "dark"/"light" preference is stored in
// localStorage; its absence means "follow the OS". Applied by stamping
// data-theme on <html>, which the dark token overrides in index.css key off
// (with prefers-color-scheme covering the no-preference case). This stays out of
// the backend/SSE — theme is a pure local visual preference.
export type Theme = "dark" | "light";

const KEY = "ultraflow-theme";

// The explicit preference, or null when following the OS.
export function storedTheme(): Theme | null {
  const v = localStorage.getItem(KEY);
  return v === "dark" || v === "light" ? v : null;
}

export function systemTheme(): Theme {
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

// The theme actually on screen right now: the stored preference, else the OS.
export function activeTheme(): Theme {
  return storedTheme() ?? systemTheme();
}

// Set an explicit preference. Stamps data-theme so the CSS overrides apply, and
// fires "themechange" so live JS consumers (the xterm terminal) re-read colors.
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem(KEY, theme);
  document.dispatchEvent(new CustomEvent("themechange"));
}
