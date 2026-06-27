import { Suspense } from "solid-js";
import { Router } from "@solidjs/router";
import { A } from "@solidjs/router";
import { FileRoutes } from "@solidjs/start/router";
import "./app.css";
import SearchBar from "./components/SearchBar";
import ThemeToggle from "./components/ThemeToggle";
import Logo from "./assets/meaningful-logo.svg?component-solid";

export default function App() {
  return (
    <Router
      root={(props) => (
        <div class="app-layout">
          <header class="app-layout__header">
            <A href="/" class="app-layout__logo" aria-label="Home">
              <Logo aria-hidden="true" />
            </A>

            <SearchBar />

            <ThemeToggle />
          </header>

          <main class="app-layout__main">
            <Suspense
              fallback={
                <div class="word-card__loading">Loading content...</div>
              }
            >
              {props.children}
            </Suspense>
          </main>

          <footer class="app-layout__footer">
            <p>Meaningful Dictionary • Built using SolidJS + Go</p>
          </footer>
        </div>
      )}
    >
      <FileRoutes />
    </Router>
  );
}
