import { Suspense } from "solid-js";
import { Router } from "@solidjs/router";
import { FileRoutes } from "@solidjs/start/router";
import "./app.css";
import SearchBar from "./components/SearchBar";

export default function App() {
  return (
    <Router
      root={(props) => (
        <div class="app-layout">
          <header class="app-layout__header">
            <SearchBar />
          </header>

          <main class="app-layout__main">
            <Suspense
              fallback={
                <div style="color: white; padding: 2rem;">
                  Loading content...
                </div>
              }
            >
              {props.children}
            </Suspense>
          </main>

          <footer class="app-layout__footer">
            <p>Meaningful Dictionary • Built with SolidStart</p>
          </footer>
        </div>
      )}
    >
      <FileRoutes />
    </Router>
  );
}
