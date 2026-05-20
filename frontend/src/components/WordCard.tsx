import { createResource, Show, For, Switch, Match, ErrorBoundary } from "solid-js";
import { components } from "../types/schema";

type WordEntry = components["schemas"]["WordEntry"];

interface WordCardProps {
  word: string;
}

/**
 * Server-side fetcher for word definitions.
 * Runs exclusively on the SolidStart Node.js server, keeping the
 * internal backend URL out of the client bundle.
 */
const fetchWord = async (word: string): Promise<WordEntry | null> => {
  "use server";
  if (!word) return null;

  const baseUrl = process.env.BACKEND_URL || "http://backend:8080";

  // We send the word exactly as it is in the URL (e.g. Apple_Tree_Flat)
  // No encoding needed as underscores are URL-safe.
  const response = await fetch(`${baseUrl}/api/word/${word}`);

  if (!response.ok) {
    if (response.status === 404) return null;
    throw new Error(`Failed to fetch "${word}" (HTTP ${response.status})`);
  }
  return response.json();
};

export default function WordCard(props: WordCardProps) {
  const [data] = createResource(() => props.word, fetchWord);

  return (
    <ErrorBoundary
      fallback={(err) => (
        <div class="word-card__loading">
          Error loading "{props.word}": {err.message}
        </div>
      )}
    >
      <Switch>
        {/* State: Loading / Refreshing */}
        <Match when={data.loading}>
          <div class="word-card__loading">Loading "{props.word}"...</div>
        </Match>

        {/* State: Resolved but null (404 / empty) */}
        <Match when={data.state === "ready" && !data()}>
          <div class="word-card__loading">
            Word "{props.word}" not found in dictionary.
          </div>
        </Match>

        {/* State: Data available — use callback child for type narrowing */}
        <Match when={data()}>
          {(entry) => (
            <div class="word-card">
              <header class="word-card__header">
                <h1 class="word-card__title">{entry().word}</h1>
                <Show when={entry().frequency > 0}>
                  <span class="word-card__frequency">
                    Zipf: {entry().frequency.toFixed(2)}
                  </span>
                </Show>
              </header>

              <Show when={entry().etymologies?.length}>
                <p class="word-card__etymology">{entry().etymologies![0]}</p>
              </Show>

              <div class="word-card__senses-container">
                <For each={Object.keys(entry().senses)}>
                  {(pos) => (
                    <section class="word-card__pos-section">
                      <h2 class="word-card__pos-title">{pos}</h2>
                      <ol class="word-card__definition-list">
                        <For each={entry().senses[pos]}>
                          {(def) => (
                            <li class="word-card__definition-item">
                              <span>{def.definition}</span>
                              <Show when={def.examples?.length}>
                                <For each={def.examples!}>
                                  {(example) => (
                                    <span class="word-card__example">
                                      "{example}"
                                    </span>
                                  )}
                                </For>
                              </Show>
                            </li>
                          )}
                        </For>
                      </ol>
                    </section>
                  )}
                </For>
              </div>
            </div>
          )}
        </Match>
      </Switch>
    </ErrorBoundary>
  );
}
