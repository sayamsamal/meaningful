import { createSignal, onMount } from "solid-js";
import MoonIcon from "../assets/moon.svg?component-solid";
import SunIcon from "../assets/sun.svg?component-solid";

type Theme = "light" | "dark";

const currentTheme = (): Theme =>
  (document.documentElement.getAttribute("data-theme") as Theme) || "light";

/**
 * Light/dark theme toggle. The active theme is applied to <html data-theme> before
 * first paint by the inline script in entry-server.tsx. Both icons are always rendered
 * and CSS (keyed on :root[data-theme]) shows the correct one — so the icon is right at
 * first paint with no flash and no hydration mismatch. The signal here only drives the
 * (non-visual) aria-label/title and the click handler.
 */
export default function ThemeToggle() {
  const [theme, setTheme] = createSignal<Theme>("light");

  // After hydration, read the real theme off <html> so the label matches.
  onMount(() => setTheme(currentTheme()));

  const toggle = () => {
    const next: Theme = theme() === "dark" ? "light" : "dark";
    setTheme(next);
    document.documentElement.setAttribute("data-theme", next);
    try {
      localStorage.setItem("theme", next);
    } catch (e) {
      /* ignore unavailable storage */
    }
  };

  const label = () =>
    theme() === "dark" ? "Switch to light mode" : "Switch to dark mode";

  return (
    <button
      type="button"
      class="theme-toggle"
      onClick={toggle}
      aria-label={label()}
      title={label()}
    >
      {/* Moon — shown in light mode (click to go dark) */}
      <MoonIcon class="theme-toggle__icon theme-toggle__icon--moon" aria-hidden="true" />
      {/* Sun — shown in dark mode (click to go light) */}
      <SunIcon class="theme-toggle__icon theme-toggle__icon--sun" aria-hidden="true" />
    </button>
  );
}
