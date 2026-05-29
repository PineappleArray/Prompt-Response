// Shared design tokens for the app. Extracted from the original inline
// definition in App.tsx so the chat and metrics views stay visually
// consistent. Spread `cssVars` onto a top-level container and reference the
// variables (e.g. var(--accent)) from inline styles.
import type { CSSProperties } from "react";

export const cssVars: Record<string, string> = {
  "--bg-primary": "#f5f3ef",
  "--bg-secondary": "#eae7e1",
  "--bg-tertiary": "#dedad3",
  "--bg-hover": "#e4e0d9",
  "--border-color": "rgba(0,0,0,0.08)",
  "--text-primary": "#2c2924",
  "--text-secondary": "#6b6560",
  "--text-muted": "#9a948e",
  "--accent": "#c96442",
  "--accent-hover": "#b5573a",
  "--user-bubble": "#e4e0d9",
  "--dropdown-bg": "#eae7e1",
  "--input-bg": "#eae7e1",
};

// Literal palette values, for places that can't read CSS variables (e.g.
// recharts props that expect concrete colors).
export const palette = {
  bgPrimary: "#f5f3ef",
  bgSecondary: "#eae7e1",
  bgTertiary: "#dedad3",
  border: "rgba(0,0,0,0.08)",
  textPrimary: "#2c2924",
  textSecondary: "#6b6560",
  textMuted: "#9a948e",
  accent: "#c96442",
  accentHover: "#b5573a",
};

export const fontStack = "'Söhne', 'Helvetica Neue', sans-serif";

// Common page-shell style shared by both views.
export const shellStyle: CSSProperties = {
  ...cssVars,
  width: "100%",
  height: "100vh",
  display: "flex",
  flexDirection: "column",
  background: "var(--bg-primary)",
  fontFamily: fontStack,
  color: "var(--text-primary)",
  overflow: "hidden",
  position: "relative",
};
